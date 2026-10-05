package archied

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/app/servicekit"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/secret"
)

type imageChange struct {
	Previous  string `json:"previous"`
	Candidate string `json:"candidate"`
}

// RunUpdateImage journals and switches the control-plane image policy. It only
// changes the image field, preserving concurrent edits to unrelated settings.
func RunUpdateImage(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("update-image", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "bootstrap config")
	journal := flags.String("journal", "", "transaction directory")
	candidate := flags.String("prepare", "", "candidate image digest")
	rollback := flags.Bool("rollback", false, "restore the prior image")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := updateImage(ctx, *configPath, *journal, *candidate, *rollback); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func updateImage(ctx context.Context, configPath, journal, candidate string, rollback bool) error {
	_, doc, err := servicekit.Resolve(slog.New(slog.DiscardHandler), configPath, "")
	if err != nil {
		return err
	}
	client, cleanup, err := servicekit.StateStoreClient(presence.Daemon, doc.Config.Services, secret.NewRegistry())
	if err != nil {
		return err
	}
	defer cleanup()
	rpc := client.ControlPlane()
	response, err := rpc.Query(ctx, &pb.QueryRequest{Kind: controlplane.ContainerRuntimePoliciesKind})
	if err != nil {
		return err
	}
	var policy map[string]json.RawMessage
	if err := json.Unmarshal(response.Resource.ValueJson, &policy); err != nil {
		return err
	}
	var current string
	if err := json.Unmarshal(policy["image"], &current); err != nil {
		return err
	}
	path := filepath.Join(journal, "image.json")
	if candidate != "" {
		return prepareImageChange(path, current, candidate)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var change imageChange
	if err := json.Unmarshal(data, &change); err != nil {
		return err
	}
	expected, desired := change.Previous, change.Candidate
	if rollback {
		expected, desired = desired, expected
	}
	if current == desired {
		return nil
	}
	if current != expected {
		return errors.New("image policy changed during update; refusing to overwrite it")
	}
	policy["image"], err = json.Marshal(desired)
	if err != nil {
		return err
	}
	data, err = json.Marshal(policy)
	if err != nil {
		return err
	}
	_, err = rpc.Command(ctx, &pb.CommandRequest{Kind: controlplane.ContainerRuntimePoliciesKind, Command: "replace", ValueJson: data, ExpectedVersion: response.Resource.Version, Actor: "self-update", Source: "installer", RequestId: fmt.Sprintf("self-update-%x-%d", sha256.Sum256(data), response.Resource.Version)})
	return err
}

func prepareImageChange(path, current, candidate string) error {
	if !regexp.MustCompile(`^([a-zA-Z0-9./:_-]+@)?sha256:[a-f0-9]{64}$`).MatchString(candidate) {
		return errors.New("candidate image must be pinned by digest")
	}
	data, err := json.Marshal(imageChange{Previous: current, Candidate: candidate})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
