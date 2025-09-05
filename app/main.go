package main

import (
	"crypto/sha1"
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

	infoStart int
	infoEnd   int
}

func NewBencodeDecoder(data []byte) *BencodeDecoder {
	return &BencodeDecoder{
		data: data,
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

		if key == "info" {
			d.infoStart = d.pos
		}

		value, err := d.decodeBencode()
		if err != nil {
			return nil, err
		}

		if key == "info" {
			d.infoEnd = d.pos
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

	switch command {
	case "decode":
		bencodedValue := os.Args[2]
		decoder := NewBencodeDecoder([]byte(bencodedValue))

		decoded, err := decoder.decodeBencode()
		if err != nil {
			fmt.Println(err)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))

	case "info":
		torrent := os.Args[2]

		file, err := os.ReadFile(torrent)
		if err != nil {
			fmt.Println(err)
			return
		}

		decoder := NewBencodeDecoder(file)
		decoded, err := decoder.decodeDictionary()
		if err != nil {
			fmt.Println(err)
			return
		}

		info := decoded["info"].(map[string]any)
		infoBytes := file[decoder.infoStart:decoder.infoEnd]
		hash := sha1.Sum(infoBytes)

		fmt.Println("Tracker URL:", decoded["announce"])
		fmt.Println("Length:", info["length"])
		fmt.Printf("Info Hash: %x\n", hash)
		fmt.Println("Piece Length:", info["piece length"])

		fmt.Println("Piece Hashes:")
		pieces := info["pieces"].(string)
		pieceBytes := []byte(pieces)
		pieceIdx := 0
		for pieceIdx < len(pieceBytes) {
			fmt.Printf("%x\n", pieceBytes[pieceIdx:pieceIdx+20])
			pieceIdx += 20
		}

	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
