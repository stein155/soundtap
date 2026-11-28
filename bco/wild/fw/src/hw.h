/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* WiLD core hardware: the register block at 0x05000000 and the RF transceiver behind its serial
 * bus (ADR 0031). Names are ours; only the values are hardware facts. */
#ifndef HW_H
#define HW_H

#define REG(o) (*(volatile unsigned *)(0x05000000u + (o)))

#define R_IRQ_ENABLE   0x208
#define R_IRQ_CLEAR    0x20c
#define R_FIQ_ENABLE   0x308
#define R_FIQ_CLEAR    0x30c
#define R_HOST_IRQ     0x590 /* write 0x8000: interrupt the host after a mailbox message */
#define R_TIMER        0x610 /* free-running, 26 bits used */
#define R_RF_CMD       0xa04 /* bit 15 start, bit 14 write, low bits the RF register */
#define R_RF_WDATA     0xa08
#define R_RF_RDATA     0xa0c
#define R_RF_STATUS    0xa10 /* bit 0 done; 0x1f0f any done or error bit */
#define R_RF_ACK       0xa14 /* write 1 to clear status bits */

/* RF waits, in R_TIMER ticks (~1 us: xport.c's TX_LOST of 200000 is ~200 ms). An RF that never
 * signals done must fail the operation, not hang the core.
 * RF_BUS_WAIT: one serial-bus transaction, the limit rf_xfer has always given it; a raw
 *   transaction (raw_wait) is the same bus, and a VCO cap measurement (tune.c cap_meas) gets as
 *   long: ~20 ms, inside the app's 200 ms per-channel scan budget beside the ~112 ms listen.
 * RF_MEAS_WAIT: the oscillator measurement at boot (rf_measure), 0xffff reference cycles: as long
 *   as rf_power_up's own 100000-tick settle delay, well inside the 3 s the app waits for the
 *   core's ready message. */
#define RF_BUS_WAIT    0x4e00
#define RF_MEAS_WAIT   100000

void delay(unsigned ticks);
int rf_xfer(unsigned cmd);
unsigned rf_read(unsigned reg);
int rf_write(unsigned reg, unsigned val);
void rf_tune(unsigned bandcode, unsigned chan);
void scan_cmd(unsigned type, const unsigned char *payload, unsigned len);
void rf_rmw(unsigned reg, unsigned val, unsigned mask);
void rf_page(unsigned page);

struct rfcal {
    unsigned count;          /* (m1 + m2) >> 3 of the two oscillator measurements */
    int delta;
    unsigned mclass, sclass; /* measurement class 0..2, calibration-search class 0..2 */
    unsigned r23, r24, r25;  /* RF regs read back after conditioning */
    unsigned done;
};
extern struct rfcal rfcal;
extern unsigned reg148;

int rf_power_up(void);
void irq_off(void);
void irq_on(void);
void copy_bytes(void *dst, const void *src, unsigned n);

/* Ring header; the host's struct wring (bco/app/src/wring.h) has the same layout. */
struct ring {
    unsigned peer, core_base, base, size, esize;
    unsigned char nch, pad[7];
    struct { unsigned shared, lim, pos; } ch[5];
};
void ring_init(struct ring *r, unsigned base, unsigned size, unsigned esize, unsigned nch);
unsigned ring_avail(struct ring *r, unsigned c);
unsigned ring_space(struct ring *r, unsigned c);
int ring_take(struct ring *r, unsigned c, unsigned n, unsigned *slot);
void ring_commit(struct ring *r, unsigned c, unsigned n);
void ring_copy(struct ring *r, unsigned c, unsigned n, void *buf, int dir);

/* Transport (xport.c): host rings, host-memory DMA, commands in, events out. */
#define MOD_HOST 0x01
#define MOD_STA  0x12
struct hostblocks { struct ring *cmd, *evt, *frm, *sup; };
void xport_init(const struct hostblocks *hb);
void xport_poll(void);
void xport_irq(void);

/* The kinds of frame RC_APTYPE and RC_APNFTYPE count (mac.c ap_kind): RTS, ACK, probe request,
 * null-data (QoS or not), other data, anything else. */
enum { AP_RTS, AP_ACK, AP_PROBE, AP_NULL, AP_DATA, AP_OTHER, RC_APTYPES };

/* Diag flight recorder (xport.c): plain counters in our SRAM that the app reads directly over its
 * SRAM window (address >> 2 published in HOST_FLAG2), so it works even when events, DMA or
 * schedule() are starved. Indices are shared with bco/app/src/wifi.c. */
enum { RC_POLL, RC_PHASE, RC_SCHED, RC_WIN, RC_LIMPOS = RC_WIN + 8, RC_DMAGO, RC_DMADONE,
       RC_DMABUSY, RC_IRQIN, RC_IRQOUT, RC_IRQST, RC_ST1, RC_ST2, RC_ST2DONE, RC_ST10, RC_FIQIN,
       RC_FIQOUT, RC_FAULTLR, RC_FAULT, RC_CMD, RC_CMDBAD, RC_EVDROP, RC_APIN, RC_APNOFILT,
       RC_APKEEP, RC_APFCS, RC_APFRATE, RC_APDATA, RC_APDRATE, RC_APNOROOM,
       RC_APACK, RC_APACKLAST, RC_APTYPE, RC_APNFTYPE = RC_APTYPE + RC_APTYPES,
       RC_APBLOST = RC_APNFTYPE + RC_APTYPES, RC_CTS, RC_N };
_Static_assert(RC_APIN == 29 && RC_APNOFILT == 30 && RC_APKEEP == 31 && RC_APNOROOM == 36 && RC_APACKLAST == 38 &&
               RC_APTYPE == 39 && RC_APNFTYPE == 45 && RC_APBLOST == 51,
               "bco/app/src/wifi.c (CORE_RC_APIN, CORE_RC_APTYPE) reads the ap_mode counters at these indices");
/* RC_FAULT: start.S vector number (start.S writes RC_FAULTLR/RC_FAULT by index: append only);
 * RC_CMDBAD: command records skipped as malformed (xport.c commands) or station commands
 * too short for their type (sta.c); RC_EVDROP: events dropped on a full event ring (xport.c).
 * In ap_mode (mac.c): RC_APIN frames to our address, RC_APNOFILT those of them the address
 * filter did not flag, RC_APKEEP a client's data frames kept for the app; RC_APFCS frames to
 * our address with a bad FCS and RC_APFRATE the last one's {rate index, rssi, frame control},
 * RC_APDATA a client's data frames with a body (FCS good) and RC_APDRATE the last one's, and
 * RC_APNOROOM those that found the receive queue full; RC_APACK the ACKs sent in ap_mode and
 * RC_APACKLAST the last one's {rate class, received rate index, frame control acknowledged};
 * RC_APTYPE + k the frames to our address of kind k (RC_APIN by kind) and RC_APNFTYPE + k those of
 * them the address filter did not flag (RC_APNOFILT by kind); RC_APBLOST the beacons given back
 * as failed because the MAC raised no TX-done for them (xport.c TX_LOST); RC_CTS the CTS frames
 * sent in answer to an RTS (mac.c, not reported to the app). The app reads these
 * through HOST_FLAG2 for AP_STATUS (bco/app/src/wifi.c), at the offsets named there. */
extern volatile unsigned rec[RC_N];
void route(unsigned type, unsigned module, unsigned src, const void *payload, unsigned len);
void sta_init(unsigned char *key_block);
unsigned peer_slot(const unsigned char *mac);
unsigned sta_cmd(unsigned type, const unsigned char *payload, unsigned len, unsigned module,
                 unsigned src);
int rf_init(void);
void mac_init(void);
extern unsigned rx_chan;
extern const unsigned char *const opparams;
#define own_mac (opparams + 3)
#define OP_CWMIN (*(const unsigned short *)(opparams + 0x38))
void mac_send_mgmt(void *desc, unsigned backoff);
extern volatile unsigned tx_done_t;
unsigned lcg(void);
int mac_ccmp_tx(const void *hdr, void *body, unsigned n, const unsigned char *tk, unsigned long long pn);
int mac_ccmp_rx(const void *hdr, const void *ct, void *out, unsigned n, const unsigned char *tk,
                unsigned long long pn);
extern volatile unsigned tx_acked;   /* the AP ACKed a frame to us (mac.c) */
extern volatile unsigned mgmt_tx_pending;    /* 0x10|ring when a host frame rides the mgmt queue */
const unsigned char *sta_txkey(unsigned slot, unsigned long long *pn);
const unsigned char *sta_rxkey(unsigned slot, int group, unsigned keyid, unsigned long long **rxpn,
                               int *soft);
void scan_poll(void);
extern volatile unsigned dwell_fired, dwell_state;
extern unsigned joined;
extern unsigned ap_mode;
extern unsigned char join_bssid[6];
extern unsigned sta_mgmt_cls;
void dwell_extend(void);
extern unsigned short resp_class[16];
unsigned rate_code(unsigned r, int long_pre);
unsigned airtime(unsigned cls, unsigned len); /* xport.c: us on air of len bytes at rate code cls */
unsigned next_seq(void);                       /* xport.c: the next 12-bit sequence number we send */
const unsigned char *rx_peek(void);
void rx_pop(void);
void tx_mac_start(unsigned q, void *desc);  /* mac.c: hand a software descriptor to queue q */
extern volatile unsigned tx_done_q;          /* FIQ sets 0x10 | queue when a host TX completes */

#endif
