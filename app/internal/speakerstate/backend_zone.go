// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"strings"

	"github.com/stein155/soundtap/app/internal/speaker"
)

func (s *Speaker) GetZone(ctx context.Context) (*speaker.Zone, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.lock()
	defer s.unlock()
	z := &speaker.Zone{Master: strings.TrimSpace(s.zoneMaster)}
	for _, m := range s.zoneMembers {
		id := strings.TrimSpace(m.deviceID)
		if id == "" {
			continue
		}
		z.Members = append(z.Members, speaker.Member{DeviceID: id, IP: strings.TrimSpace(m.ip)})
	}
	return z, nil
}

func (s *Speaker) SetZone(ctx context.Context, master speaker.Member, slaves []speaker.Member) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lock()
	s.zoneMaster = master.DeviceID
	s.zoneSenders = master.IP
	s.zoneMembers = append([]member{{deviceID: master.DeviceID, ip: master.IP}}, toMembers(slaves)...)
	s.unlock()
	return nil
}

func (s *Speaker) AddZoneSlave(ctx context.Context, master speaker.Member, slaves []speaker.Member) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lock()
	if s.zoneMaster == "" {
		s.zoneMaster = master.DeviceID
		s.zoneSenders = master.IP
		s.zoneMembers = append(s.zoneMembers, member{deviceID: master.DeviceID, ip: master.IP})
	}
	for _, m := range toMembers(slaves) {
		if !s.hasMember(m.deviceID) {
			s.zoneMembers = append(s.zoneMembers, m)
		}
	}
	s.unlock()
	return nil
}

func (s *Speaker) RemoveZoneSlave(ctx context.Context, _ speaker.Member, slaves []speaker.Member) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	drop := map[string]bool{}
	for _, m := range slaves {
		drop[m.DeviceID] = true
	}
	s.lock()
	kept := s.zoneMembers[:0:0]
	for _, m := range s.zoneMembers {
		if !drop[m.deviceID] {
			kept = append(kept, m)
		}
	}
	s.zoneMembers = kept
	if len(s.zoneMembers) <= 1 {
		s.zoneMaster, s.zoneSenders, s.zoneMembers = "", "", nil
	}
	s.unlock()
	return nil
}

func toMembers(in []speaker.Member) []member {
	out := make([]member, 0, len(in))
	for _, m := range in {
		out = append(out, member{deviceID: strings.TrimSpace(m.DeviceID), ip: strings.TrimSpace(m.IP)})
	}
	return out
}
