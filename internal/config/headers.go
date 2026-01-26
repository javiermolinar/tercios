package config

import (
	"fmt"
	"strings"
)

type HeaderFlags struct {
	values map[string]string
}

func (h *HeaderFlags) String() string {
	return ""
}

func (h *HeaderFlags) Set(value string) error {
	if h.values == nil {
		h.values = make(map[string]string)
	}
	key, val, ok := strings.Cut(value, "=")
	if !ok {
		key, val, ok = strings.Cut(value, ":")
	}
	if !ok {
		return fmt.Errorf("header must be in Key=Value or Key: Value form")
	}
	key = strings.TrimSpace(key)
	val = strings.TrimSpace(val)
	if key == "" || val == "" {
		return fmt.Errorf("header must include non-empty key and value")
	}
	h.values[key] = val
	return nil
}

func (h *HeaderFlags) Values() map[string]string {
	if h.values == nil {
		return map[string]string{}
	}
	return h.values
}
