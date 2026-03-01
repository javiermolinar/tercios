package model

import "context"

type BatchExporter interface {
	ExportBatch(ctx context.Context, batch Batch) error
	Shutdown(ctx context.Context) error
}

type BatchExporterFactory interface {
	NewBatchExporter(ctx context.Context) (BatchExporter, error)
}
