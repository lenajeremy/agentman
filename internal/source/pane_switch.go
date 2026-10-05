package source

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/tmux"
)

// paneDriver is how an adapter switches a session's mode or model in its
// terminal: read the pane, press named keys, type a line, press one literal
// key. Every step it takes is checked on screen before the next, and each
// function is replaceable so a switch can be tested against real captures.
type paneDriver struct {
	capture func(ctx context.Context, name string) (string, error)
	// keys presses named keys only (tmux.SendKeys).
	keys func(ctx context.Context, name string, keys ...string) error
	// line clears the prompt, types text and submits it (tmux.Send).
	line func(ctx context.Context, name, text string) error
	// literal types one key as text: a menu's shortcut letter (tmux.Answer).
	literal func(ctx context.Context, name, key string) error
	// settle is how long a screen is given to redraw after a key.
	settle time.Duration
}

func newPaneDriver() paneDriver {
	return paneDriver{
		capture: tmux.Capture,
		keys:    tmux.SendKeys,
		line:    tmux.Send,
		literal: tmux.Answer,
		settle:  60 * time.Millisecond,
	}
}

// await reads the pane until check accepts it, for about two seconds.
func (d paneDriver) await(ctx context.Context, name string, check func(pane string) bool) (string, error) {
	var pane string
	for attempt := 0; attempt < 30; attempt++ {
		if attempt > 0 || d.settle > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(d.settle):
			}
		}
		var err error
		pane, err = d.capture(ctx, name)
		if err != nil {
			return "", err
		}
		if check(pane) {
			return pane, nil
		}
	}
	return pane, errors.New("the screen did not show what was expected")
}

// paneRow is one numbered row of a picker.
type paneRow struct {
	number  int
	label   string
	focused bool
}

// pickerRowPattern reads a numbered picker row: an optional marker (the
// focus, or a scroll arrow), the number, then the label up to the gap before
// its description.
var pickerRowPattern = regexp.MustCompile(`^\s*([❯›↑↓])?\s*(\d+)\.\s+(.+?)(?:\s{2,}|$)`)

// pickerRows reads every numbered row in lines, with focus marked by marker.
func pickerRows(lines []string, marker string) []paneRow {
	var rows []paneRow
	for _, line := range lines {
		match := pickerRowPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		number, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		rows = append(rows, paneRow{
			number: number, label: strings.TrimSpace(match[3]), focused: match[1] == marker,
		})
	}
	return rows
}

// focusRow moves a picker's focus onto the row whose label want accepts, one
// key at a time, reading the focus back after each and giving up after limit
// presses. rows reads the picker from a capture and reports false once the
// expected picker is no longer on screen. A row scrolled out of sight is
// looked for below first, then above.
func (d paneDriver) focusRow(
	ctx context.Context, name string, want func(label string) bool,
	rows func(pane string) ([]paneRow, bool), limit int,
) (string, error) {
	down := true
	for presses := 0; ; presses++ {
		pane, err := d.capture(ctx, name)
		if err != nil {
			return "", err
		}
		visible, ok := rows(pane)
		if !ok {
			return "", errors.New("the picker closed")
		}
		focused, target := -1, -1
		for index, row := range visible {
			if row.focused {
				focused = index
			}
			if want(row.label) {
				target = index
			}
		}
		if focused < 0 {
			return "", errors.New("the picker shows no focus")
		}
		if focused == target {
			return pane, nil
		}
		if presses >= limit {
			return "", errors.New("the choice was not found in the picker")
		}
		if target >= 0 {
			down = visible[target].number > visible[focused].number
		} else if focused == len(visible)-1 && visible[focused].number > 1 && down {
			// At the bottom with the row still unseen: it is above.
			down = false
		}
		key := "Up"
		if down {
			key = "Down"
		}
		before := visible[focused].number
		if err := d.keys(ctx, name, key); err != nil {
			return "", err
		}
		_, err = d.await(ctx, name, func(pane string) bool {
			now, ok := rows(pane)
			if !ok {
				return true
			}
			for _, row := range now {
				if row.focused {
					return row.number != before
				}
			}
			return false
		})
		if err != nil {
			// The focus did not move: the end of the list.
			if target < 0 && down {
				down = false
				continue
			}
			return "", fmt.Errorf("the focus did not move: %w", err)
		}
	}
}

// closePicker backs out of a picker that is verified to be open, so a failed
// switch does not leave it on the user's screen.
func (d paneDriver) closePicker(ctx context.Context, name string, open func(pane string) bool, presses int) {
	for range presses {
		pane, err := d.capture(ctx, name)
		if err != nil || !open(pane) {
			return
		}
		if d.keys(ctx, name, "Escape") != nil {
			return
		}
		time.Sleep(d.settle)
	}
}

// bottomLines returns the last n lines of a pane that hold any text.
func bottomLines(pane string, n int) []string {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	var kept []string
	for index := len(lines) - 1; index >= 0 && len(kept) < n; index-- {
		if strings.TrimSpace(lines[index]) != "" {
			kept = append(kept, lines[index])
		}
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept
}
