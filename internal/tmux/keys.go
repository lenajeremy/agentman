package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// namedKeys are the only keys SendKeys will press: the ones a CLI's menus and
// pickers are driven with. Cursor offers "Add to allowlist" on Tab and "Run
// Everything" on shift+Tab; Kiro and Antigravity move through lists with the
// arrows. Nothing here types text, stops a process — Ctrl-C is Interrupt's,
// deliberately — or reaches tmux's own prefix bindings.
//
// M-j is the one modified key. Antigravity opens its subagent panel on
// alt+j, and that panel is the only place a subagent's permission request
// can be answered. It is listed by name: no other alt key is allowed.
var namedKeys = map[string]bool{
	"Tab": true, "BTab": true, "Enter": true, "Escape": true, "Space": true,
	"Up": true, "Down": true, "Left": true, "Right": true,
	"M-j": true,
}

// maxNamedKeys bounds one call. A menu is a handful of rows; a long burst is
// a bug, and every key of it would land.
const maxNamedKeys = 32

// keyInterval is the pause between two keys. Ink and React TUIs update focus
// between key events, and a burst lets every event see the same stale focus
// (see moveFocus).
const keyInterval = 30 * time.Millisecond

// SendKeys presses named keys in an Agentman pane, one at a time, in order.
//
// Every key is checked before the first is pressed, so a call with one key
// that is not allowed presses none of them: half a menu navigation is worse
// than none. Held under the pane's action lock, so a send or an answer from
// another phone cannot land between two of them.
func SendKeys(ctx context.Context, name string, keys ...string) error {
	if err := checkKeys(name, keys); err != nil {
		return err
	}
	if !Available() {
		return ErrNotInstalled
	}
	lock := actionLock(name)
	lock.Lock()
	defer lock.Unlock()
	for index, key := range keys {
		if index > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(keyInterval):
			}
		}
		if _, err := run(ctx, keyArgs(name, key)...); err != nil {
			return fmt.Errorf("tmux: could not press %s: %w", key, err)
		}
	}
	return nil
}

// checkKeys refuses a call before any key is pressed.
//
// The pane must be one Agentman started: these keys answer prompts, and a
// pane the user opened for something else is not ours to answer.
func checkKeys(name string, keys []string) error {
	if len(name) <= len(Prefix) || !strings.HasPrefix(name, Prefix) {
		return errors.New("tmux: refusing to press keys in a session Agentman did not start")
	}
	if len(keys) == 0 {
		return errors.New("tmux: no keys given")
	}
	if len(keys) > maxNamedKeys {
		return fmt.Errorf("tmux: refusing to press %d keys at once", len(keys))
	}
	for _, key := range keys {
		if !namedKeys[key] {
			return fmt.Errorf("tmux: %q is not a key Agentman presses", key)
		}
	}
	return nil
}

// keyArgs is one key press. Without -l, so tmux reads the key's name rather
// than typing its letters — which is why only names from namedKeys get here.
func keyArgs(name, key string) []string {
	return []string{"send-keys", "-t", name, key}
}
