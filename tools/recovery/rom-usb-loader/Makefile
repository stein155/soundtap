# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

CC ?= cc
PKG_CONFIG ?= pkg-config
CFLAGS ?= -O2
CFLAGS += -std=c11 -Wall -Wextra -Wpedantic -Werror
CPPFLAGS += $(shell $(PKG_CONFIG) --cflags libusb-1.0)
LDLIBS += $(shell $(PKG_CONFIG) --libs libusb-1.0)
CC_TARGETS_APPLE := $(shell $(CC) -dM -E -x c /dev/null 2>/dev/null | grep -c '__APPLE__')
ifeq ($(CC_TARGETS_APPLE),0)
CPPFLAGS += $(shell $(PKG_CONFIG) --cflags openssl)
LDLIBS += $(shell $(PKG_CONFIG) --libs openssl)
endif

.PHONY: all clean test

all: rom-usb-loader

rom-usb-loader: rom-usb-loader.o protocol.o
	$(CC) $(LDFLAGS) -o $@ $^ $(LDLIBS)

test_protocol: test_protocol.o protocol.o
	$(CC) $(LDFLAGS) -o $@ $^

test_stage: test_stage.o protocol.o
	$(CC) $(LDFLAGS) -o $@ $^ $(LDLIBS)

test: rom-usb-loader test_protocol test_stage
	./test_protocol
	./test_stage
	./test_loader.sh ./rom-usb-loader
	sh ./test_udev_rules.sh
	sh ./test_timing.sh rom-usb-loader.c

clean:
	rm -f rom-usb-loader test_protocol test_stage *.o *.plist
