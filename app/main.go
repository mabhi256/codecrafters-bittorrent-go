package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"unicode"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

type BencodeDecoder struct {
	data []byte
	pos  int
}

func NewBencodeDecoder(data string) *BencodeDecoder {
	return &BencodeDecoder{
		data: []byte(data),
		pos:  0,
	}
}

func (d *BencodeDecoder) decodeBencode() (any, error) {
	switch {
	case unicode.IsDigit(rune(d.data[d.pos])):
		return d.decodeString()

	case d.data[d.pos] == 'i':
		return d.decodeInteger()

	case d.data[d.pos] == 'l':
		return d.decodeList()

	case d.data[d.pos] == 'd':
		return d.decodeDictionary()

	default:
		return "", fmt.Errorf("format not supported")

	}
}

func (d *BencodeDecoder) decodeString() (string, error) {
	start := d.pos

	for d.pos < len(d.data) {
		if d.data[d.pos] == ':' {
			break
		}
		d.pos++
	}

	lengthStr := string(d.data[start:d.pos])

	length, err := strconv.Atoi(lengthStr)
	if err != nil {
		return "", err
	}

	d.pos++
	value := d.data[d.pos : d.pos+length]
	d.pos += length

	return string(value), nil
}

func (d *BencodeDecoder) decodeInteger() (int, error) {
	d.pos++ // move pos beyond the beginning delimiter

	sign := 1
	if string(d.data[d.pos]) == "-" {
		sign = -1
		d.pos++
	}

	var valueStr string

	for d.pos < len(d.data) && string(d.data[d.pos]) != "e" {
		valueStr += string(d.data[d.pos])
		d.pos++
	}

	if valueStr == "-0" {
		return 0, fmt.Errorf("invalid integer encoding: i-0e")
	}

	if valueStr[0] == '0' && len(valueStr) != 1 {
		return 0, fmt.Errorf("invalid integer encoding: %s", d.data)
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return 0, err
	}
	d.pos++

	return sign * value, nil
}

func (d *BencodeDecoder) decodeList() ([]any, error) {
	d.pos++

	list := []any{}
	for d.pos < len(d.data) && d.data[d.pos] != 'e' {
		item, err := d.decodeBencode()
		if err != nil {
			return nil, err
		}
		list = append(list, item)
	}

	d.pos++
	return list, nil
}

func (d *BencodeDecoder) decodeDictionary() (map[string]any, error) {
	d.pos++

	dict := make(map[string]any)
	for d.pos < len(d.data) && d.data[d.pos] != 'e' {
		key, err := d.decodeString()
		if err != nil {
			return nil, err
		}

		value, err := d.decodeBencode()
		if err != nil {
			return nil, err
		}

		dict[key] = value
	}

	d.pos++
	return dict, nil
}

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	// fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	if command == "decode" {
		bencodedValue := os.Args[2]
		decoder := NewBencodeDecoder(bencodedValue)

		decoded, err := decoder.decodeBencode()
		if err != nil {
			fmt.Println(err)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
