package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

type usage struct {
	cpu  float64
	mem  float64
	disk float64
	free uint64
	ok   bool
}

func readUsage(ctx context.Context) usage {
	var u usage
	if pct, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pct) > 0 {
		u.cpu, u.ok = pct[0], true
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		u.mem, u.ok = vm.UsedPercent, true
	}
	if du, err := disk.UsageWithContext(ctx, diskPath()); err == nil {
		u.disk, u.free, u.ok = du.UsedPercent, du.Free, true
	}
	return u
}

func diskPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

func (u usage) facts() [][2]string {
	if !u.ok {
		return nil
	}
	return [][2]string{
		{"CPU", fmt.Sprintf("%s%.0f%%[-]", usageColor(u.cpu), u.cpu)},
		{"MEM", fmt.Sprintf("%s%.0f%%[-]", usageColor(u.mem), u.mem)},
		{"DISK", fmt.Sprintf("%s%.0f%%[-] [gray](%s free)[-]", usageColor(u.disk), u.disk, humanBytes(u.free))},
	}
}

func usageColor(pct float64) string {
	switch {
	case pct >= 90:
		return "[red]"
	case pct >= 70:
		return "[yellow]"
	}
	return "[white]"
}

func humanBytes(b uint64) string {
	const unit = 1024
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(b)
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", b, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func pollUsage(ctx context.Context, every time.Duration, onSample func(usage)) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	onSample(readUsage(ctx))
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			onSample(readUsage(ctx))
		}
	}
}
