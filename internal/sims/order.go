package sims

import (
	"cmp"
	"strings"

	"github.com/siner308/sims/internal/device"
)

func DefaultOrder(a, b device.Device) int {
	return cmp.Or(
		a.StateRank()-b.StateRank(),
		b.LastActiveAt.Compare(a.LastActiveAt),
		strings.Compare(b.Name, a.Name),
		strings.Compare(b.Runtime, a.Runtime),
	)
}
