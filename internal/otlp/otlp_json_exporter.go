package otlp

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/javiermolinar/tercios/internal/model"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type otlpJSONBatchExporter struct {
	writer io.Writer
	lock   *sync.Mutex
}

func (e *otlpJSONBatchExporter) ExportBatch(_ context.Context, batch model.Batch) error {
	if len(batch) == 0 {
		return nil
	}

	// Reuse the live export conversion. The protobuf bridge keeps pdata's
	// representation separate while its JSON encoder handles OTLP-specific
	// rules such as hex IDs and numeric enums.
	request := &collectortracepb.ExportTraceServiceRequest{ResourceSpans: modelBatchToProto(batch)}
	wire, err := proto.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal OTLP request: %w", err)
	}
	pdataRequest := ptraceotlp.NewExportRequest()
	if err := pdataRequest.UnmarshalProto(wire); err != nil {
		return fmt.Errorf("decode OTLP request into pdata: %w", err)
	}
	payload, err := pdataRequest.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal OTLP JSON request: %w", err)
	}
	payload = append(payload, '\n')

	// All exporters created by the factory share this lock, so every line is
	// a complete ExportTraceServiceRequest even with concurrent workers.
	e.lock.Lock()
	defer e.lock.Unlock()
	n, err := e.writer.Write(payload)
	if err == nil && n != len(payload) {
		err = io.ErrShortWrite
	}
	return err
}

func (e *otlpJSONBatchExporter) Shutdown(_ context.Context) error {
	return nil
}
