package main

import (
	"fmt"
	"sort"
	"strconv"
)

type BencodeEncoder struct {
	data []byte
}

func NewBencodeEncoder() *BencodeEncoder {
	return &BencodeEncoder{
		data: make([]byte, 0),
	}
}

func (e *BencodeEncoder) encode(value any) error {
	switch v := value.(type) {
	case string:
		return e.encodeString(v)

	case int:
		return e.encodeInteger(v)

	case []any:
		return e.encodeList(v)

	case map[string]any:
		return e.encodeDict(v)

	default:
		return fmt.Errorf("unsupported type: %T", value)
	}
}

func (e *BencodeEncoder) encodeString(value string) error {
	length := strconv.Itoa(len(value))
	e.data = append(e.data, []byte(length)...)
	e.data = append(e.data, ':')
	e.data = append(e.data, []byte(value)...)

	return nil
}

func (e *BencodeEncoder) encodeInteger(value int) error {
	e.data = append(e.data, 'i')
	valueStr := strconv.Itoa(value)
	e.data = append(e.data, []byte(valueStr)...)
	e.data = append(e.data, 'e')

	return nil
}

func (e *BencodeEncoder) encodeList(values []any) error {
	e.data = append(e.data, 'l')

	for _, value := range values {
		err := e.encode(value)
		if err != nil {
			return err
		}
	}
	e.data = append(e.data, 'e')

	return nil
}

func (e *BencodeEncoder) encodeDict(values map[string]any) error {
	e.data = append(e.data, 'd')

	// Get keys and sort them
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Iterate in sorted order
	for _, key := range keys {
		err := e.encode(key)
		if err != nil {
			return err
		}

		err = e.encode(values[key])
		if err != nil {
			return err
		}
	}
	e.data = append(e.data, 'e')

	return nil
}
