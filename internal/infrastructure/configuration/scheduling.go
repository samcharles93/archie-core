package configuration

import (
	"time"
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
