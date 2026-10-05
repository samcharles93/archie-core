package archied

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

func RunReleaseChannel(args []string, in io.Reader, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("select-release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	channel := flags.String("channel", "stable", "stable, next, or exact-pin")
	pin := flags.String("pin", "", "exact release version")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	data, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	version, err := releaseupdate.SelectRelease(strings.Fields(string(data)), *channel, *pin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(out, version)
	return 0
}
