// Package policy reconciles the registry custom-policy definitions, their
// activation bindings, and the Libraries policies to their source-of-truth
// folders. Ports scripts/reconcile-registry-policies.sh, reconcile-bindings.py
// and reconcile-library-policies.py.
package policy

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var nameLineRe = regexp.MustCompile(`(?m)^name:[ \t]*(.+?)[ \t]*$`)

// readName extracts the top-level `name:` from a manifest (an indented
// "- name:" under parameters is not matched).
func readName(text string) string {
	m := nameLineRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(m[1]), `"'`)
}

type cmdError struct {
	code int
	cmd  []string
}

func (e *cmdError) Error() string {
	return fmt.Sprintf("chainctl failed (%d): %s", e.code, strings.Join(e.cmd, " "))
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}

// run logs the command and, in apply mode, executes it — returning a cmdError on
// non-zero exit (aborting the reconcile, like `set -e`). In plan mode it only
// prints the command.
func run(apply bool, cmd []string) error {
	fmt.Println("    $ " + strings.Join(cmd, " "))
	if !apply {
		return nil
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return &cmdError{exitCode(err), cmd}
	}
	return nil
}

// capture runs a command and returns (stdout, stderr, exitCode).
func capture(cmd ...string) (string, string, int) {
	c := exec.Command(cmd[0], cmd[1:]...)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	return out.String(), errb.String(), exitCode(err)
}

// gitRemoved returns dir/*.yaml files deleted between prev and cur.
func gitRemoved(prev, cur, dir string) []string {
	out, _, _ := capture("git", "diff", "--name-only", "--diff-filter=D", prev, cur, "--", dir+"/*.yaml")
	var res []string
	for _, ln := range strings.Split(out, "\n") {
		if strings.TrimSpace(ln) != "" {
			res = append(res, ln)
		}
	}
	return res
}

func gitShow(prev, rel string) string {
	out, _, code := capture("git", "show", prev+":"+rel)
	if code != 0 {
		return ""
	}
	return out
}
