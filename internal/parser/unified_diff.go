package parser

import (
	"fmt"
	"strings"
)

// Cursor's ACP channel reports an edit as the file's text before and after
// ({"type":"diff","path","oldText","newText"}), not as a patch. The app colours
// unified diffs, so this turns the pair into one.

// diffContext is the unchanged lines kept around each change, as git does.
const diffContext = 3

// diffMaxCells bounds the line-matching table. Edits an agent makes are small
// against files of a few thousand lines; beyond this the change is shown as
// the whole old text replaced by the whole new one, which is still correct.
const diffMaxCells = 4_000_000

// UnifiedDiff renders the change from oldText to newText at path as a unified
// diff. An empty oldText is a new file and an empty newText a deletion.
func UnifiedDiff(path, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	before, after := diffLines(oldText), diffLines(newText)
	oldName, newName := "a/"+strings.TrimPrefix(path, "/"), "b/"+strings.TrimPrefix(path, "/")
	if oldText == "" {
		oldName = "/dev/null"
	}
	if newText == "" {
		newName = "/dev/null"
	}
	ops := diffOps(before, after)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s", oldName, newName)
	for _, hunk := range diffHunks(ops) {
		out.WriteString("\n" + hunk)
	}
	return out.String()
}

func diffLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	text string
	// oldLine and newLine are the 1-based positions this op sits at.
	oldLine, newLine int
}

// diffOps matches lines by longest common subsequence.
func diffOps(before, after []string) []diffOp {
	n, m := len(before), len(after)
	if n*m > diffMaxCells {
		ops := make([]diffOp, 0, n+m)
		for i, line := range before {
			ops = append(ops, diffOp{kind: '-', text: line, oldLine: i + 1, newLine: 1})
		}
		for j, line := range after {
			ops = append(ops, diffOp{kind: '+', text: line, oldLine: n + 1, newLine: j + 1})
		}
		return ops
	}
	// lcs[i][j] is the common length of before[i:] and after[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if before[i] == after[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && before[i] == after[j]:
			ops = append(ops, diffOp{kind: ' ', text: before[i], oldLine: i + 1, newLine: j + 1})
			i++
			j++
		// On a tie the removal comes first, so a changed line reads as
		// "-old" then "+new", the way every diff tool prints it.
		case j < m && (i == n || lcs[i][j+1] > lcs[i+1][j]):
			ops = append(ops, diffOp{kind: '+', text: after[j], oldLine: i + 1, newLine: j + 1})
			j++
		default:
			ops = append(ops, diffOp{kind: '-', text: before[i], oldLine: i + 1, newLine: j + 1})
			i++
		}
	}
	return ops
}

// diffHunks groups changes with their context into "@@ -a,b +c,d @@" hunks.
func diffHunks(ops []diffOp) []string {
	var hunks []string
	for start := 0; start < len(ops); {
		// Find the next change.
		first := start
		for first < len(ops) && ops[first].kind == ' ' {
			first++
		}
		if first == len(ops) {
			break
		}
		from := max(start, first-diffContext)
		// Extend through changes separated by little enough context.
		end := first
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*diffContext {
				end = min(run, end+diffContext)
				break
			}
			end = run
		}
		var body strings.Builder
		oldCount, newCount := 0, 0
		for _, op := range ops[from:end] {
			body.WriteString("\n" + string(op.kind) + op.text)
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
		}
		oldStart, newStart := ops[from].oldLine, ops[from].newLine
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		hunks = append(hunks, fmt.Sprintf("@@ -%d,%d +%d,%d @@%s", oldStart, oldCount, newStart, newCount, body.String()))
		start = end
	}
	return hunks
}
