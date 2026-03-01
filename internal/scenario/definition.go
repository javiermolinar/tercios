package scenario

import (
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

type Service struct {
	ID                 string
	ResourceAttributes map[string]attribute.Value
}

type Node struct {
	ID       string
	Service  string
	SpanName string
}

type Edge struct {
	From           string
	To             string
	Kind           EdgeKind
	Repeat         int
	Duration       time.Duration
	SpanAttributes map[string]attribute.Value
}

type Definition struct {
	Name     string
	Seed     int64
	Root     string
	Services map[string]Service
	Nodes    map[string]Node
	Edges    []Edge
}

func (c Config) Build() (Definition, error) {
	if err := c.Validate(); err != nil {
		return Definition{}, err
	}

	definition := Definition{
		Name:     c.Name,
		Seed:     c.Seed,
		Root:     c.Root,
		Services: make(map[string]Service, len(c.Services)),
		Nodes:    make(map[string]Node, len(c.Nodes)),
		Edges:    make([]Edge, 0, len(c.Edges)),
	}

	for id, service := range c.Services {
		attrs, err := typedMapToAttributes(service.Resource)
		if err != nil {
			return Definition{}, fmt.Errorf("service %s: %w", id, err)
		}
		definition.Services[id] = Service{ID: id, ResourceAttributes: attrs}
	}

	for id, node := range c.Nodes {
		definition.Nodes[id] = Node{ID: id, Service: node.Service, SpanName: node.SpanName}
	}

	for i, edge := range c.Edges {
		spanAttrs, err := typedMapToAttributes(edge.SpanAttributes)
		if err != nil {
			return Definition{}, fmt.Errorf("edge %d: %w", i, err)
		}
		definition.Edges = append(definition.Edges, Edge{
			From:           edge.From,
			To:             edge.To,
			Kind:           edge.Kind,
			Repeat:         edge.Repeat,
			Duration:       time.Duration(edge.DurationMs) * time.Millisecond,
			SpanAttributes: spanAttrs,
		})
	}

	return definition, nil
}

func typedMapToAttributes(values map[string]TypedValue) (map[string]attribute.Value, error) {
	if len(values) == 0 {
		return map[string]attribute.Value{}, nil
	}

	result := make(map[string]attribute.Value, len(values))
	for key, value := range values {
		attrValue, err := typedValueToAttributeValue(value)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", key, err)
		}
		result[key] = attrValue
	}
	return result, nil
}

func typedValueToAttributeValue(value TypedValue) (attribute.Value, error) {
	return value.ToAttributeValue()
}
