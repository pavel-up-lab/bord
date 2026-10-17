package security

import (
	"bytes"
	"net/netip"
	"sort"
	"time"
)

type Ban struct {
	ID        string
	IP        *netip.Addr
	Prefix    *netip.Prefix
	Lookup    []byte
	Board     string
	Operation string
	ExpiresAt time.Time
}

type BanSubject struct {
	IP        netip.Addr
	Lookup    []byte
	Board     string
	Operation string
}

func EvaluateBans(bans []Ban, subject BanSubject, now time.Time) (Ban, bool) {
	type match struct {
		ban   Ban
		score int
	}
	matches := make([]match, 0)
	for _, ban := range bans {
		if !ban.ExpiresAt.After(now) || ban.IP != nil && *ban.IP != subject.IP || ban.Prefix != nil && !ban.Prefix.Contains(subject.IP) || len(ban.Lookup) > 0 && !bytes.Equal(ban.Lookup, subject.Lookup) || ban.Board != "" && ban.Board != subject.Board || ban.Operation != "" && ban.Operation != subject.Operation {
			continue
		}
		score := 0
		if ban.IP != nil {
			score += 1000
		}
		if len(ban.Lookup) > 0 {
			score += 900
		}
		if ban.Prefix != nil {
			score += 500 + ban.Prefix.Bits()
		}
		if ban.Board != "" {
			score += 100
		}
		if ban.Operation != "" {
			score += 50
		}
		matches = append(matches, match{ban: ban, score: score})
	}
	if len(matches) == 0 {
		return Ban{}, false
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].ban.ID < matches[j].ban.ID
		}
		return matches[i].score > matches[j].score
	})
	return matches[0].ban, true
}
