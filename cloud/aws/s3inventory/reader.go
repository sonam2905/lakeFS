package s3inventory

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/scritchley/orc"
	"github.com/treeverse/lakefs/logging"
	s3parquet "github.com/xitongsys/parquet-go-source/s3"
	"github.com/xitongsys/parquet-go/reader"
)

const (
	OrcFormatName     = "ORC"
	ParquetFormatName = "Parquet"
)

var (
	ErrUnsupportedInventoryFormat = errors.New("unsupported inventory type. supported types: parquet, orc")
)

type IReader interface {
	GetFileReader(format string, bucket string, key string) (FileReader, error)
	GetMetadataReader(format string, bucket string, key string) (MetadataReader, error)
}

type InventoryObject struct {
	Bucket             string
	Key                string
	IsLatest           *bool
	IsDeleteMarker     *bool
	Size               *int64
	LastModifiedMillis *int64
	Checksum           *string
}

func (o *InventoryObject) GetPhysicalAddress() string {
	return "s3://" + o.Bucket + "/" + o.Key
}

type Reader struct {
	ctx    context.Context
	svc    s3iface.S3API
	logger logging.Logger
}

type MetadataReader interface {
	GetNumRows() int64
	Close() error
	FirstObjectKey() string
	LastObjectKey() string
}

type FileReader interface {
	MetadataReader
	Read(n int) ([]InventoryObject, error)
}

func NewReader(ctx context.Context, svc s3iface.S3API, logger logging.Logger) IReader {
	return &Reader{ctx: ctx, svc: svc, logger: logger}
}

func (o *Reader) GetFileReader(format string, bucket string, key string) (FileReader, error) {
	switch format {
	case OrcFormatName:
		return o.getOrcReader(bucket, key, false)
	case ParquetFormatName:
		return o.getParquetReader(bucket, key)
	default:
		return nil, ErrUnsupportedInventoryFormat
	}
}

func (o *Reader) GetMetadataReader(format string, bucket string, key string) (MetadataReader, error) {
	switch format {
	case OrcFormatName:
		return o.getOrcReader(bucket, key, true)
	default:
		return o.GetFileReader(format, bucket, key)
	}
}

func (o *Reader) getParquetReader(bucket string, key string) (FileReader, error) {
	pf, err := s3parquet.NewS3FileReaderWithClient(o.ctx, o.svc, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create parquet file reader: %w", err)
	}
	pr, err := reader.NewParquetReader(pf, nil, 4)
	if err != nil {
		return nil, fmt.Errorf("failed to create parquet reader: %w", err)
	}
	return &ParquetInventoryFileReader{ParquetReader: *pr}, nil
}

func (o *Reader) getOrcReader(bucket string, key string, tailOnly bool) (FileReader, error) {
	orcFile, err := DownloadOrc(o.ctx, o.svc, o.logger, bucket, key, tailOnly)
	if err != nil {
		return nil, err
	}
	orcReader, err := orc.NewReader(orcFile)
	if err != nil {
		return nil, err
	}
	orcSelect := getOrcSelect(orcReader.Schema())
	return &OrcInventoryFileReader{
		ctx:       o.ctx,
		reader:    orcReader,
		orcFile:   orcFile,
		orcSelect: orcSelect,
		cursor:    orcReader.Select(orcSelect.SelectFields...),
	}, nil
}
