package scenario

import (
	_ "embed"
	"strings"
)

//go:embed default_scenario.json
var defaultScenarioJSON string

// DefaultGenerator returns a batch generator using the embedded default scenario.
// The runSeed controls trace/span ID namespacing (0 = auto-random per process).
func DefaultGenerator(runSeed int64) (BatchGenerator, error) {
	cfg, err := DecodeJSON(strings.NewReader(defaultScenarioJSON))
	if err != nil {
		return nil, err
	}
	definition, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	runSalt := deriveRunSalt(runSeed)
	definition.Seed = int64(namespaceSeed(definition.Seed, runSalt, 1))
	return NewGenerator(definition), nil
}
