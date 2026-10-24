package manager

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The user's own permission rules, on top of the built-in policy: which
// low-risk kinds Agentman may approve, and rules that allow a command or
// path, or always ask about it, everywhere or in one folder.
//
// An "ask" rule wins over everything. An "allow" rule can approve what the
// built-in policy would leave for the user, such as `git push` in one
// repository, but never what touches keys or credentials, nor anything run
// as root: those wait for the user whatever the rules say.

const maxRules = 100

var ruleKinds = []string{"command", "edit", "read", "network", "any"}

// policy is what classify decides with: the kinds switched on, and the
// user's rules.
type policy struct {
	kinds protocol.ApproveKinds
	rules []protocol.PermissionRule
}

// allKinds is every low-risk kind switched on: the default, and what an
// older settings file without the switches means.
var allKinds = protocol.ApproveKinds{Reads: true, Edits: true, Commands: true, Continue: true}

func policyOf(settings protocol.ManagerSettings) policy {
	p := policy{kinds: allKinds, rules: settings.Rules}
	if settings.Approve != nil {
		p.kinds = *settings.Approve
	}
	return p
}

// match is the first rule with action that covers a request of kind on
// target, from an agent working in folder.
func (p policy) match(action, kind, target, folder string) (protocol.PermissionRule, bool) {
	for _, rule := range p.rules {
		if rule.Action != action || (rule.Kind != "any" && rule.Kind != kind) {
			continue
		}
		if rule.Folder != "" && !insideFolder(folder, expandHome(strings.TrimSpace(rule.Folder))) {
			continue
		}
		if ruleCovers(rule, kind, target, folder) {
			return rule, true
		}
	}
	return protocol.PermissionRule{}, false
}

// ruleCovers reports whether rule's pattern matches the target: a command
// as written, a path inside the folder by its relative path as well as its
// absolute one, a URL as written.
func ruleCovers(rule protocol.PermissionRule, kind, target, folder string) bool {
	pattern := strings.TrimSpace(rule.Pattern)
	target = strings.TrimSpace(target)
	if globMatch(pattern, target) {
		return true
	}
	if kind == "edit" || kind == "read" {
		path := strings.Trim(target, `"'`)
		if !filepath.IsAbs(path) && folder != "" {
			path = filepath.Join(folder, path)
		}
		path = filepath.Clean(path)
		if globMatch(expandHome(pattern), path) {
			return true
		}
		if folder != "" && insideFolder(path, folder) {
			if relative, err := filepath.Rel(filepath.Clean(folder), path); err == nil && globMatch(pattern, relative) {
				return true
			}
		}
	}
	return false
}

// describe is a rule in a few words, for the reason a request waits.
func describe(rule protocol.PermissionRule) string {
	text := fmt.Sprintf("%s `%s`", rule.Action, rule.Pattern)
	if rule.Folder != "" {
		text += " in " + filepath.Base(expandHome(rule.Folder))
	}
	return text
}

// globMatch matches text against a pattern where * stands for any run of
// characters, slashes and spaces included, and ? for any one.
func globMatch(pattern, text string) bool {
	if pattern == "" {
		return false
	}
	p, t := []rune(pattern), []rune(text)
	// Classic two-pointer wildcard matching, backtracking to the last *.
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// runsAsRoot reports a command part no rule may approve.
func runsAsRoot(part string) bool {
	words := strings.Fields(part)
	return len(words) > 0 && (words[0] == "sudo" || words[0] == "su" || words[0] == "doas")
}

func validateRules(rules []protocol.PermissionRule) error {
	if len(rules) > maxRules {
		return fmt.Errorf("keep it to %d rules", maxRules)
	}
	for _, rule := range rules {
		if rule.Action != "allow" && rule.Action != "ask" {
			return fmt.Errorf("a rule is either allow or ask, not %q", rule.Action)
		}
		if !slices.Contains(ruleKinds, rule.Kind) {
			return fmt.Errorf("a rule is for %s, not %q", strings.Join(ruleKinds, ", "), rule.Kind)
		}
		if strings.TrimSpace(rule.Pattern) == "" || len(rule.Pattern) > 300 {
			return fmt.Errorf("a rule needs a pattern of up to 300 characters")
		}
		if strings.Trim(rule.Pattern, "*? ") == "" && rule.Action == "allow" {
			return fmt.Errorf("an allow rule needs more than wildcards: `%s` would approve everything", rule.Pattern)
		}
		if folder := strings.TrimSpace(rule.Folder); folder != "" && !filepath.IsAbs(folder) && !strings.HasPrefix(folder, "~") {
			return fmt.Errorf("a rule's folder is a full path, not %q", rule.Folder)
		}
	}
	return nil
}

// checkPermission answers what Agentman would do with a request, for the
// Settings' "Try it", by putting it to the policy as an agent would ask it.
func checkPermission(settings protocol.ManagerSettings, kind, target, folder string) protocol.PermissionCheck {
	d := decide(protocol.Session{Cwd: strings.TrimSpace(expandHome(folder))}, kind, strings.TrimSpace(target), "yes", policyOf(settings))
	check := protocol.PermissionCheck{Auto: d.auto && settings.AutoApprove, Summary: d.summary, Reason: d.reason}
	if d.auto && !settings.AutoApprove {
		check.Reason = "approving for you is switched off"
	}
	return check
}
