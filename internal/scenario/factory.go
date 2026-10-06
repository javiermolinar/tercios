package scenario

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"time"
)

func NewBatchGeneratorFromFiles(paths []string, strategy SelectionStrategy) (BatchGenerator, error) {
	return NewBatchGeneratorFromFilesWithRunSeed(paths, strategy, 0)
}

func NewBatchGeneratorFromFilesWithRunSeed(paths []string, strategy SelectionStrategy, runSeed int64) (BatchGenerator, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one scenario file is required")
	}

	runSalt := deriveRunSalt(runSeed)
	definitions := make([]Definition, 0, len(paths))
	selectionSeed := uint64(0)
	for i, path := range paths {
		cfg, err := LoadFromJSON(path)
		if err != nil {
			return nil, fmt.Errorf("invalid scenario file %q: %w", path, err)
		}
		definition, err := cfg.Build()
		if err != nil {
			return nil, fmt.Errorf("invalid scenario definition in %q: %w", path, err)
		}

		namespacedSeed := namespaceSeed(definition.Seed, runSalt, uint64(i+1))
		definition.Seed = int64(namespacedSeed)
		definitions = append(definitions, definition)
		selectionSeed ^= namespacedSeed ^ (uint64(i+1) * 0x9e3779b97f4a7c15)
	}

	if len(definitions) == 1 {
		return NewGenerator(definitions[0]), nil
	}

	return NewMultiGenerator(definitions, strategy, int64(selectionSeed))
}

func namespaceSeed(seed int64, runSalt uint64, index uint64) uint64 {
	return splitmix64(uint64(seed) ^ runSalt ^ (index * 0x9e3779b97f4a7c15))
}

func deriveRunSalt(runSeed int64) uint64 {
	if runSeed != 0 {
		return uint64(runSeed)
	}

	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return binary.BigEndian.Uint64(bytes[:])
	}

	return uint64(time.Now().UnixNano()) ^ (uint64(os.Getpid()) * 0x9e3779b97f4a7c15)
}
