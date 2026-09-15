package ui

import (
	"strings"
	"time"
)

// The mascot from the README logo, four rows high, with the legs redrawn each tick so it runs in place.
var runnerFrames = [][]string{
	{
		`  .-----.    `,
		`  |>_ >_| (( `,
		`  | \_/ |    `,
		`  _/   |     `,
	},
	{
		`  .-----.    `,
		`  |>_ >_|  ( `,
		`  | \_/ |    `,
		`   |   |     `,
	},
	{
		`  .-----.    `,
		`  |>_ >_| (( `,
		`  | \_/ |    `,
		`   |   \_    `,
	},
	{
		`  .-----.    `,
		`  |>_ >_|  ( `,
		`  | \_/ |    `,
		`   |   |     `,
	},
}

const spinnerInterval = 160 * time.Millisecond

// renderRunner puts the message beside the mascot on its second row.
func renderRunner(frame int, msg string) string {
	f := runnerFrames[frame%len(runnerFrames)]
	var b strings.Builder
	for i, line := range f {
		b.WriteString("[yellow]")
		b.WriteString(line)
		b.WriteString("[-]")
		if i == 1 {
			b.WriteString("  ")
			b.WriteString(msg)
		}
		if i < len(f)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
