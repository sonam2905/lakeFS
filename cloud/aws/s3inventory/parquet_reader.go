package s3inventory

import (
	"fmt"

	"github.com/cznic/mathutil"
	"github.com/go-openapi/swag"
	"github.com/spf13/cast"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/reader"
	"github.com/xitongsys/parquet-go/schema"
)

type ParquetInventoryFileReader struct {
	*reader.ParquetReader
	nextRow            int64
	fieldToParquetPath map[string]string
}

func NewParquetInventoryFileReader(parquetReader *reader.ParquetReader) (*ParquetInventoryFileReader, error) {
	fieldToParquetPath := getParquetPaths(parquetReader.SchemaHandler)
	for _, required := range requiredFields {
		if _, ok := fieldToParquetPath[required]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrRequiredFieldNotFound, required)
		}
	}
	return &ParquetInventoryFileReader{
		ParquetReader:      parquetReader,
		fieldToParquetPath: fieldToParquetPath,
	}, nil
}

func (p *ParquetInventoryFileReader) Close() error {
	p.ReadStop()
	return p.PFile.Close()
}

func (p *ParquetInventoryFileReader) getKeyColumnStatistics() *parquet.Statistics {
	for i, c := range p.Footer.RowGroups[0].Columns {
		if c.MetaData.PathInSchema[len(c.GetMetaData().GetPathInSchema())-1] == "Key" {
			return p.Footer.RowGroups[0].Columns[i].GetMetaData().GetStatistics()
		}
	}
	return p.Footer.RowGroups[0].Columns[1].GetMetaData().GetStatistics()
}
func (p *ParquetInventoryFileReader) FirstObjectKey() string {
	return string(p.getKeyColumnStatistics().GetMin())
}

func (p *ParquetInventoryFileReader) LastObjectKey() string {
	return string(p.getKeyColumnStatistics().GetMax())
}

func (p *ParquetInventoryFileReader) Read(n int) ([]*InventoryObject, error) {
	num := mathutil.MinInt64(int64(n), p.GetNumRows()-p.nextRow)
	p.nextRow += num
	res := make([]*InventoryObject, num)
	for fieldName, path := range p.fieldToParquetPath {
		columnRes, _, dls, err := p.ReadColumnByPath(path, num)
		if err != nil {
			return nil, fmt.Errorf("failed to read parquet column %s: %w", fieldName, err)
		}
		for i, v := range columnRes {
			if !isRequired(fieldName) && dls[i] == 0 {
				// got no value for non-required field, move on
				continue
			}
			if res[i] == nil {
				res[i] = new(InventoryObject)
			}
			err := set(res[i], fieldName, v)
			if err != nil {
				return nil, fmt.Errorf("failed to read parquet column %s: %w", fieldName, err)
			}
		}
	}
	return res, nil
}

func set(o *InventoryObject, f string, v interface{}) error {
	var err error
	switch f {
	case bucketFieldName:
		var bucket string
		bucket, err = cast.ToStringE(v)
		o.Bucket = bucket
	case keyFieldName:
		var key string
		key, err = cast.ToStringE(v)
		o.Key = key
	case isLatestFieldName:
		var isLatest bool
		isLatest, err = cast.ToBoolE(v)
		o.IsLatest = swag.Bool(isLatest)
	case isDeleteMarkerFieldName:
		var isDeleteMarker bool
		isDeleteMarker, err = cast.ToBoolE(v)
		o.IsDeleteMarker = swag.Bool(isDeleteMarker)
	case sizeFieldName:
		var size int64
		size, err = cast.ToInt64E(v)
		o.Size = swag.Int64(size)
	case lastModifiedDateFieldName:
		var lastModifiedMillis int64
		lastModifiedMillis, err = cast.ToInt64E(v)
		o.LastModifiedMillis = swag.Int64(lastModifiedMillis)
	case eTagFieldName:
		var checksum string
		checksum, err = cast.ToStringE(v)
		o.Checksum = swag.String(checksum)
	}
	return err
}

// getParquetPaths returns parquet schema fields as a mapping from their base column name to their path in ParquetReader
// only known inventory fields are returned
func getParquetPaths(schemaHandler *schema.SchemaHandler) map[string]string {
	res := make(map[string]string)
	for i, fieldInfo := range schemaHandler.Infos {
		for _, field := range inventoryFields {
			if fieldInfo.ExName == field {
				res[field] = schemaHandler.IndexMap[int32(i)]
			}
		}
	}
	return res
}
