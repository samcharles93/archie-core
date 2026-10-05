package archied

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/sys/unix"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// RunUpdateTopology observes the host using the candidate's topology contract.
func RunUpdateTopology(args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("update-topology", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "bootstrap config file")
	releasePath := flags.String("release", "", "release manifest")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if err := checkUpdateTopology(*configPath, *releasePath, out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func checkUpdateTopology(configPath, releasePath string, out io.Writer) error {
	data, err := os.ReadFile(releasePath)
	if err != nil {
		return err
	}
	var manifest struct {
		Required releaseupdate.Topology `json:"required_topology"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if len(manifest.Required.Units) == 0 {
		return errors.New("release has no required_topology")
	}
	for _, unit := range manifest.Required.Units {
		if !regexp.MustCompile(`^archie[a-z0-9-]*$`).MatchString(unit) {
			return fmt.Errorf("invalid release unit %q", unit)
		}
	}
	data, err = os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := toml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("observe topology: %w", err)
	}
	sections := make(map[string]bool)
	for _, section := range manifest.Required.ConfigSections {
		sections[section] = hasConfigSection(document, section)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, observeErr := exec.CommandContext(ctx, "systemctl", "--user", "list-unit-files", "archie*.service", "--no-legend", "--no-pager").Output()
	units := observedUnits(output, observeErr)
	plan := manifest.Required.MigrationPlan(units, sections)
	if len(plan) != 0 {
		fmt.Fprintln(out, "Required migration, in order:")
		for _, step := range plan {
			fmt.Fprintln(out, " -", step)
		}
		if observeErr != nil {
			return fmt.Errorf("topology unknown; apply the printed migration before retrying: %w", observeErr)
		}
		return errors.New("refusing partial installation; apply the printed migration before retrying")
	}
	return nil
}

func hasConfigSection(document map[string]any, section string) bool {
	for part := range strings.SplitSeq(section, ".") {
		next, ok := document[part].(map[string]any)
		if !ok {
			return false
		}
		document = next
	}
	return true
}

func updateStateDir() string {
	if dir := os.Getenv("ARCHIE_UPDATE_STATE_DIR"); dir != "" {
		return dir
	}
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "archie", "update")
}

// Recovery runs independently of the daemon unit: it must be able to stop that
// unit and replace its executable without systemd killing the recovery process.
func resumeUpdate(ctx context.Context) (bool, error) {
	journal := filepath.Join(updateStateDir(), "transaction")
	if _, err := os.Stat(journal); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	lock, err := os.OpenFile(filepath.Join(updateStateDir(), "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	cmd := exec.CommandContext(ctx, "systemd-run", "--user", "--collect", "--quiet", "--unit=archied-update-recovery", "--setenv=ARCHIE_UPDATE_STATE_DIR="+updateStateDir(), filepath.Join(journal, "watchdog"), "--boot-recover")
	if output, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("schedule update recovery: %w: %s", err, output)
	}
	return true, nil
}

func observedUnits(output []byte, err error) map[string]bool {
	units := make(map[string]bool)
	if err != nil {
		return units
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] != "masked" {
			units[strings.TrimSuffix(fields[0], ".service")] = true
		}
	}
	return units
}
