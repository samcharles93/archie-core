package command

import (
	"fmt"
	"os"
	"strings"
)

// Blocked reports a command line that must not run. It carries the
// offending segment so the refusal can name what was actually objected to
// rather than the whole line.
type Blocked struct {
	// Rule is a short identifier for the rule that fired.
	Rule string
	// Reason explains the refusal in one line, shown to the model and the
	// user.
	Reason string
	// Segment is the offending command as it appeared.
	Segment string
}

func (b *Blocked) Error() string {
	if b.Segment == "" {
		return fmt.Sprintf("refused (%s): %s", b.Rule, b.Reason)
	}
	return fmt.Sprintf("refused (%s): %s: %q", b.Rule, b.Reason, b.Segment)
}

// Hardline checks a command line against the rules that hold regardless of
// configuration, approval, or operator instruction.
//
// This is deliberately a floor and not a security boundary. Anything with a
// shell has countless ways around a pattern list, and pretending otherwise
// would be worse than not having one. What it does buy is that the failure
// modes with no recovery -- the ones where you cannot intervene afterwards
// because the machine, the filesystem, or the gateway itself is gone -- are
// not reachable by an ordinary mistake.
//
// Two rules exist specifically to keep intervention possible: an agent must
// not be able to stop the daemon that hosts /stop, nor start a second one
// outside the supervisor's control.
func Hardline(line string) error {
	return hardline(line, 0)
}

// maxNesting bounds how far the rules follow a command into the commands it
// wraps. Real command lines nest once or twice; the limit only stops a
// pathological input from recursing without end.
const maxNesting = 8

func hardline(line string, depth int) error {
	if line = strings.TrimSpace(line); line == "" {
		return nil
	}

	// The fork bomb is checked against the whole line because its
	// definition spans the separators everything else is split on.
	if isForkBomb(line) {
		return &Blocked{
			Rule:    "fork-bomb",
			Reason:  "exhausts the process table and requires a reboot to clear",
			Segment: line,
		}
	}

	for _, seg := range Split(line) {
		if err := checkSegment(seg, depth); err != nil {
			return err
		}
	}
	return nil
}

// checkSegment applies every rule to one command invocation, then follows
// the segment into any command it merely wraps.
//
// Without that second step the rules are trivially defeated, and not only
// by someone trying: `bash -c "rm -rf /"` is a shape models produce
// unprompted, and `xargs rm -rf /` reads as ordinary shell. In both cases
// the dangerous command is an argument, so nothing above sees it.
func checkSegment(seg Segment, depth int) error {
	for _, rule := range rules {
		if reason, hit := rule.check(seg); hit {
			return &Blocked{Rule: rule.name, Reason: reason, Segment: seg.Raw}
		}
	}

	if depth >= maxNesting {
		return nil
	}

	// A shell's -c argument is a whole command line of its own, quoting
	// and separators included, so it goes back through the front door.
	if payload, ok := shellPayload(seg); ok {
		return hardline(payload, depth+1)
	}

	// A wrapper's remaining words are one command, already tokenised.
	if inner, ok := unwrap(seg); ok {
		return checkSegment(inner, depth+1)
	}

	return nil
}

// shells run a command line supplied as an argument.
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true,
	"ksh": true, "ash": true,
}

// shellPayload returns the command line a segment asks another shell to
// run, whether via `sh -c` or `eval`.
func shellPayload(seg Segment) (string, bool) {
	base := baseName(seg.Name)

	if shells[base] {
		for i, arg := range seg.Args {
			if arg == "-c" && i+1 < len(seg.Args) {
				return seg.Args[i+1], true
			}
		}
		return "", false
	}

	if base == "eval" && len(seg.Args) > 0 {
		return strings.Join(seg.Args, " "), true
	}

	return "", false
}

// wrappers run another command rather than doing the work themselves, so
// the command that matters is further along the line.
var wrappers = map[string]bool{
	"xargs": true, "env": true, "nice": true, "ionice": true,
	"timeout": true, "stdbuf": true, "time": true, "nohup": true,
	"setsid": true, "unbuffer": true, "script": true,
}

// unwrap returns the command a wrapper invokes.
//
// Flags, environment assignments, and bare numbers are skipped, the last
// because several wrappers take a leading value -- `timeout 30 rm -rf /`
// and `nice -n 10 rm -rf /` both have to resolve to the rm.
func unwrap(seg Segment) (Segment, bool) {
	if !wrappers[baseName(seg.Name)] {
		return Segment{}, false
	}
	for i, arg := range seg.Args {
		if strings.HasPrefix(arg, "-") || isAssignment(arg) || isBareNumber(arg) {
			continue
		}
		return segmentFromWords(strings.Join(seg.Args[i:], " "), seg.Args[i:])
	}
	return Segment{}, false
}

// isBareNumber reports whether a word is a plain count or duration, such
// as timeout's 30 or 1m.
func isBareNumber(word string) bool {
	if word == "" {
		return false
	}
	trimmed := strings.TrimRight(word, "smhd")
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

type rule struct {
	name  string
	check func(Segment) (string, bool)
}

var rules = []rule{
	{"recursive-root-delete", checkRecursiveDelete},
	{"filesystem-format", checkMkfs},
	{"block-device-write", checkBlockDeviceWrite},
	{"kill-all-processes", checkKillAll},
	{"system-power", checkSystemPower},
	{"sudo-stdin-password", checkSudoStdin},
	{"daemon-self-termination", checkSelfTermination},
	{"daemon-launch", checkDaemonLaunch},
}

// criticalPaths are the delete targets with no recovery path. Anything
// outside this set is the operator's business, including recursive deletes
// inside the workspace.
var criticalPaths = map[string]bool{
	"/": true, "//": true, "/.": true, "/..": true,
	"~": true, "$HOME": true, "${HOME}": true,
	"/home": true, "/root": true, "/etc": true, "/usr": true,
	"/var": true, "/bin": true, "/sbin": true, "/boot": true,
	"/lib": true, "/lib64": true, "/opt": true, "/srv": true,
	"/proc": true, "/sys": true, "/dev": true,
}

// checkRecursiveDelete refuses a recursive rm whose target is a critical
// path. A recursive delete elsewhere is ordinary work and is allowed.
func checkRecursiveDelete(seg Segment) (string, bool) {
	if seg.Name != "rm" {
		return "", false
	}
	if !hasRecursiveFlag(seg.Args) {
		return "", false
	}
	for _, arg := range seg.Args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if isCriticalPath(arg) {
			return "recursive delete of " + arg + ", which is not recoverable", true
		}
	}
	return "", false
}

// hasRecursiveFlag reports whether rm was asked to recurse, covering both
// the long form and bundled short flags such as -rf and -fr.
func hasRecursiveFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--recursive" {
			return true
		}
		if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
			continue
		}
		if strings.ContainsAny(arg[1:], "rR") {
			return true
		}
	}
	return false
}

// isCriticalPath reports whether a delete target names the filesystem root,
// a home directory, or a system directory -- with or without a trailing
// slash or glob.
func isCriticalPath(arg string) bool {
	trimmed := strings.TrimSuffix(arg, "*")
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		// The argument was "/" or "/*".
		return true
	}
	return criticalPaths[trimmed] || criticalPaths[arg]
}

// checkMkfs refuses any filesystem format. The tool name carries the type,
// so mkfs.ext4 and mkfs alike are matched on the prefix.
func checkMkfs(seg Segment) (string, bool) {
	base := baseName(seg.Name)
	if base == "mkfs" || strings.HasPrefix(base, "mkfs.") {
		return "formats a filesystem, destroying everything on it", true
	}
	return "", false
}

// checkBlockDeviceWrite refuses writes straight to a block device, whether
// through dd's of= or a shell redirect. /dev/null and /dev/zero are
// excluded: writing to them is routine and harmless.
func checkBlockDeviceWrite(seg Segment) (string, bool) {
	if baseName(seg.Name) == "dd" {
		for _, arg := range seg.Args {
			target, ok := strings.CutPrefix(arg, "of=")
			if ok && isBlockDevice(target) {
				return "writes raw data to " + target + ", destroying the device's contents", true
			}
		}
	}
	for i, word := range seg.Words {
		if word != ">" && word != ">>" {
			continue
		}
		if i+1 < len(seg.Words) && isBlockDevice(seg.Words[i+1]) {
			return "redirects output to the block device " + seg.Words[i+1], true
		}
	}
	return "", false
}

// isBlockDevice reports whether a path names a disk device rather than one
// of the harmless character devices.
func isBlockDevice(path string) bool {
	if !strings.HasPrefix(path, "/dev/") {
		return false
	}
	switch path {
	case "/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/stdout", "/dev/stderr":
		return false
	}
	name := strings.TrimPrefix(path, "/dev/")
	for _, prefix := range []string{"sd", "nvme", "hd", "vd", "xvd", "mmcblk", "loop", "md", "dm-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// checkKillAll refuses signalling every process, which takes down the
// daemon and anything else on the machine.
func checkKillAll(seg Segment) (string, bool) {
	if baseName(seg.Name) != "kill" {
		return "", false
	}
	for _, arg := range seg.Args {
		if arg == "-1" {
			return "signals every process on the system", true
		}
	}
	return "", false
}

// powerCommands halt or restart the machine outright.
var powerCommands = map[string]bool{
	"shutdown": true, "reboot": true, "halt": true, "poweroff": true,
}

// checkSystemPower refuses anything that powers the machine down or
// restarts it. Matching is on the command position only, so a commit
// message or a log line mentioning reboot is unaffected.
func checkSystemPower(seg Segment) (string, bool) {
	base := baseName(seg.Name)
	if powerCommands[base] {
		return "shuts down or restarts the machine", true
	}
	if base == "init" || base == "telinit" {
		for _, arg := range seg.Args {
			if arg == "0" || arg == "6" {
				return "changes runlevel to shut down or reboot", true
			}
		}
	}
	if base == "systemctl" {
		for _, arg := range seg.Args {
			switch arg {
			case "poweroff", "reboot", "halt", "kexec":
				return "shuts down or restarts the machine", true
			}
		}
	}
	return "", false
}

// checkSudoStdin refuses feeding sudo a password on stdin unless one has
// been configured deliberately. Without SUDO_PASSWORD set there is nothing
// legitimate to supply, so the flag only appears when something is trying
// passwords.
func checkSudoStdin(seg Segment) (string, bool) {
	if !seg.Elevated || os.Getenv("SUDO_PASSWORD") != "" {
		return "", false
	}
	for _, flag := range seg.ElevationArgs {
		if flag == "-S" || flag == "--stdin" {
			return "supplies a sudo password on stdin and no SUDO_PASSWORD is configured", true
		}
		if strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && strings.Contains(flag[1:], "S") {
			return "supplies a sudo password on stdin and no SUDO_PASSWORD is configured", true
		}
	}
	return "", false
}

// daemonNames are the processes whose death removes the operator's ability
// to intervene.
var daemonNames = map[string]bool{"archied": true, "archie-agent": true}

// checkSelfTermination refuses killing the daemon that hosts the gateway.
//
// /stop, approvals, and every other control run inside archied. An agent
// that can stop it can put itself beyond reach, and the only remaining
// recourse is physical access to the host.
func checkSelfTermination(seg Segment) (string, bool) {
	base := baseName(seg.Name)

	if base == "pkill" || base == "killall" {
		for _, arg := range seg.Args {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if namesDaemon(arg) {
				return "kills the archie daemon, which is what /stop runs in", true
			}
		}
	}

	if base == "systemctl" || base == "service" {
		if hasAny(seg.Args, "stop", "kill", "disable", "mask", "restart") && anyNamesDaemon(seg.Args) {
			return "stops the archie daemon, which is what /stop runs in", true
		}
	}

	// The docker socket is mounted, so the container is reachable from
	// inside itself.
	if base == "docker" || base == "podman" {
		if hasAny(seg.Args, "stop", "kill", "rm", "restart", "down") && anyNamesDaemon(seg.Args) {
			return "stops the archie daemon's container, which is what /stop runs in", true
		}
	}

	return "", false
}

// checkDaemonLaunch refuses starting another daemon.
//
// A second archied outside the supervisor competes for the same Telegram
// updates and the same queues, and nothing is tracking it -- so it cannot
// be restarted, reconfigured, or stopped through any of the usual paths.
func checkDaemonLaunch(seg Segment) (string, bool) {
	name := baseName(seg.Name)
	if name == "nohup" || name == "setsid" {
		if len(seg.Args) > 0 && namesDaemon(baseName(seg.Args[0])) {
			return "starts a second archie daemon detached from the supervisor", true
		}
		return "", false
	}
	if daemonNames[name] {
		return "starts a second archie daemon outside the supervisor", true
	}
	return "", false
}

// namesDaemon reports whether an argument refers to an archie process or
// container. Container names carry a compose prefix and suffix, so this is
// a substring test rather than an exact one.
func namesDaemon(arg string) bool {
	if daemonNames[arg] {
		return true
	}
	return strings.Contains(arg, "archied") || strings.Contains(arg, "archie-agent")
}

func anyNamesDaemon(args []string) bool {
	for _, arg := range args {
		if namesDaemon(arg) {
			return true
		}
	}
	return false
}

func hasAny(args []string, wanted ...string) bool {
	for _, arg := range args {
		for _, w := range wanted {
			if arg == w {
				return true
			}
		}
	}
	return false
}

// baseName strips any directory from a command, so that /sbin/reboot and
// reboot are judged the same.
func baseName(name string) string {
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// isForkBomb reports whether the line defines and invokes a
// self-replicating function. Whitespace is removed first because it is
// optional throughout the construct.
func isForkBomb(line string) bool {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, line)
	return strings.Contains(compact, ":(){:|:&};:") ||
		strings.Contains(compact, ":(){:|:&};:&")
}
