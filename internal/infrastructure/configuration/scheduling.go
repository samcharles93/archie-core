package configuration

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// SchedulingInput is the external [scheduling] input document. It deliberately
// contains no runtime defaults or domain dependencies; application composition
// translates it into scheduling.EngineConfig.
type SchedulingInput struct {
	Interval    Duration `toml:"interval" yaml:"interval"`
	MaxParallel int      `toml:"max_parallel" yaml:"max_parallel"`
	JobTimeout  Duration `toml:"job_timeout" yaml:"job_timeout"`
	EventsSink  string   `toml:"events_sink" yaml:"events_sink"`
}

// Duration is a textual duration at the configuration boundary.
type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(value)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

const (
	EventsSinkBus  = "bus"
	EventsSinkNone = "none"
)

type schedulingDocument struct {
	Scheduling SchedulingInput `toml:"scheduling" yaml:"scheduling"`
}

func decodeSchedulingFile(path string, target *SchedulingInput) error {
	_, err := decodeSchedulingFileKeys(path, target)
	return err
}

// decodeSchedulingFileKeys is decodeSchedulingFile plus the file's
// undecoded top-level keys under this target (see decodeConfigFileKeys).
func decodeSchedulingFileKeys(path string, target *SchedulingInput) ([]string, error) {
	doc := schedulingDocument{Scheduling: *target}
	keys, err := decodeConfigFileKeys(path, &doc)
	if err != nil {
		return nil, err
	}
	*target = doc.Scheduling
	return keys, nil
}

func applySchedulingOverlay(target *SchedulingInput, overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	data, err := yaml.Marshal(overrides)
	if err != nil {
		return fmt.Errorf("%w: encoding scheduling overlay: %w", ErrUnreadable, err)
	}
	doc := schedulingDocument{Scheduling: *target}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%w: parsing scheduling overlay: %w", ErrUnreadable, err)
	}
	*target = doc.Scheduling
	return nil
}
