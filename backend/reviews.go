package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// numberPatch prefixes every line that exists in the new version of the file with its line number,
// so the model can attach comments to the right line: "  12: context", "  13:+added", "     :-removed".
func numberPatch(patch string) string {
	var b strings.Builder
	newLine := 0
	for _, l := range strings.Split(patch, "\n") {
		if m := hunkHeader.FindStringSubmatch(l); m != nil {
			newLine, _ = strconv.Atoi(m[2])
			b.WriteString(l + "\n")
			continue
		}
		switch {
		case strings.HasPrefix(l, "+"):
			fmt.Fprintf(&b, "%5d:%s\n", newLine, l)
			newLine++
		case strings.HasPrefix(l, "-"):
			fmt.Fprintf(&b, "     :%s\n", l)
		case strings.HasPrefix(l, `\`): // "\ No newline at end of file"
			b.WriteString("     :" + l + "\n")
		case l == "" && newLine == 0:
			// outside a hunk
		default:
			fmt.Fprintf(&b, "%5d: %s\n", newLine, strings.TrimPrefix(l, " "))
			newLine++
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type diffFile struct {
	Path, Status string
	Added, Del   int
	Patch        string
}

// renderDiff turns files into plain text for the model, within a size budget.
func renderDiff(files []diffFile, total int) string {
	const perFile, budget = 6000, 60000
	var b strings.Builder
	omitted := 0
	for _, f := range files {
		if b.Len() > budget {
			omitted++
			continue
		}
		fmt.Fprintf(&b, "=== %s (%s, +%d -%d)\n", f.Path, f.Status, f.Added, f.Del)
		if f.Patch == "" {
			b.WriteString("(no text diff: binary, renamed or too large)\n\n")
			continue
		}
		b.WriteString(cut(numberPatch(f.Patch), perFile) + "\n\n")
	}
	if extra := total - len(files); extra > 0 {
		omitted += extra
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d more changed files not shown)\n", omitted)
	}
	return strings.TrimSpace(b.String())
}

// cleanComments bounds and checks what the model asks to post.
func cleanComments(in []reviewComment) []reviewComment {
	out := []reviewComment{}
	for _, c := range in {
		c.Path = strings.TrimSpace(c.Path)
		c.Body = strings.TrimSpace(c.Body)
		if c.Body == "" || c.Line < 1 || c.Line > 1_000_000 || c.Path == "" || strings.HasPrefix(c.Path, "/") {
			continue
		}
		bad := false
		for _, seg := range strings.Split(c.Path, "/") {
			if seg == ".." || seg == "." || seg == "" {
				bad = true
			}
		}
		if bad {
			continue
		}
		c.Body = cut(c.Body, 4000)
		out = append(out, c)
		if len(out) == 20 {
			break
		}
	}
	return out
}

const reviewFooter = "\n\n_Automated review by Claude through Agent Workflow. It can be wrong: please check before acting on it._"

// foldComments appends inline comments that could not be attached to a line to the summary.
func foldComments(summary string, failed []reviewComment) string {
	if len(failed) == 0 {
		return summary
	}
	var b strings.Builder
	b.WriteString(summary + "\n\n**Comments that could not be attached to a line:**\n")
	for _, c := range failed {
		fmt.Fprintf(&b, "- `%s:%d` %s\n", c.Path, c.Line, strings.ReplaceAll(c.Body, "\n", " "))
	}
	return b.String()
}
