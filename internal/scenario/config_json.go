package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Reject null native fields, except parent:null (an explicit root). Existing
// string fields retain JSON null decoding; Validate still requires a service.
func (n *NodeConfig) UnmarshalJSON(data []byte) error {
	type plain NodeConfig
	var value plain
	if err := decodeStrictObject(data, &value, "parent", "span_name", "service"); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	*n = NodeConfig(value)
	return nil
}

func (n NodeConfig) hasDirectFields() bool {
	return n.Kind != nil || len(n.Parent) != 0 || n.Status != nil ||
		n.StartOffsetMs != nil || n.DurationMs != nil ||
		n.SpanAttributes != nil || n.SpanEvents != nil || n.SpanLinks != nil
}

func (s *NodeStatusConfig) UnmarshalJSON(data []byte) error {
	type plain NodeStatusConfig
	var value plain
	if err := decodeStrictObject(data, &value); err != nil {
		return fmt.Errorf("status: %w", err)
	}
	*s = NodeStatusConfig(value)
	return nil
}

func (c *Config) UnmarshalJSON(data []byte) error {
	type plain Config
	var value plain
	if err := decodeStrict(data, &value); err != nil {
		return err
	}
	fields, err := jsonFields(data)
	if err != nil {
		return err
	}
	value.rootPresent = fields["root"] != nil
	if isJSONNull(fields["root"]) {
		value.Root = ""
	}
	*c = Config(value)
	return nil
}

func (e *EdgeConfig) UnmarshalJSON(data []byte) error {
	type plain EdgeConfig
	var value plain
	fields, err := jsonFields(data)
	if err != nil {
		return err
	}
	if err := decodeStrict(data, &value); err != nil {
		return fmt.Errorf("edge from %s to %s: %w", fields["from"], fields["to"], err)
	}
	*e = EdgeConfig(value)
	e.fields = fields
	return nil
}

// Keep explicitly supplied zero fields on round-trip. In particular, an absent
// edge kind must stay absent instead of becoming kind:"", and absent repeat
// must not become the invalid repeat:0 on a span entry.
func (e EdgeConfig) MarshalJSON() ([]byte, error) {
	type plain EdgeConfig
	data, err := json.Marshal(plain(e))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for key, value := range map[string]any{
		"to": e.To, "kind": e.Kind, "repeat": e.Repeat,
		"duration_ms": e.DurationMs, "network_latency_ms": e.NetworkLatencyMs,
		"span_attributes": e.SpanAttributes, "span_events": e.SpanEvents, "span_links": e.SpanLinks,
	} {
		if e.hasField(key) && fields[key] == nil {
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			fields[key] = raw
		}
	}
	return json.Marshal(fields)
}

func (e EdgeConfig) hasField(key string) bool { _, ok := e.fields[key]; return ok }
func (e EdgeConfig) hasKind() bool            { return e.Kind != "" || e.hasField("kind") }

func (c Config) hasDirectNodes() bool {
	for _, edge := range c.Edges {
		if !edge.hasKind() {
			return true
		}
	}
	return false
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

func decodeStrictObject(data []byte, target any, nullable ...string) error {
	if err := decodeStrict(data, target); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("expected an object")
	}
	for key, raw := range fields {
		if !isJSONNull(raw) {
			continue
		}
		allowed := false
		for _, name := range nullable {
			if strings.EqualFold(key, name) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("%s cannot be null", key)
		}
	}
	return nil
}

// Match encoding/json's case-insensitive field lookup and last-key-wins
// behavior, rather than losing presence information for e.g. Repeat:0.
func jsonFields(data []byte) (map[string]json.RawMessage, error) {
	if isJSONNull(data) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	known := []string{"root", "from", "to", "kind", "repeat", "duration_ms", "network_latency_ms", "span_attributes", "span_events", "span_links"}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("expected an object key")
		}
		for _, name := range known {
			if strings.EqualFold(key, name) {
				key = name
				break
			}
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields[key] = raw
	}
	return fields, nil
}

func isJSONNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
