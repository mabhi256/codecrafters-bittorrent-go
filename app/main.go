package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"unicode"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

const PEER_ID = "mabhi12345mabhi12345"

type BencodeDecoder struct {
	data []byte
	pos  int

	infoStart int
	infoEnd   int
}

type TorrentFile struct {
	Announce string   `json:"announce"`
	Info     InfoDict `json:"info"`
	InfoHash [20]byte `json:"infoHash"`
}

type InfoDict struct {
	Length      int    `json:"length"`
	Name        string `json:"name"`
	PieceLength int    `json:"piece length"`
	Pieces      []byte `json:"pieces"`
}

type TrackerResponse struct {
	Complete    int      `json:"complete"`
	Incomplete  int      `json:"incomplete"`
	Interval    int      `json:"interval"`
	MinInterval int      `json:"min interval"`
	Peers       []string `json:"peers"`
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

func (d *BencodeDecoder) decodeTorrent() (*TorrentFile, error) {
	dict, err := d.decodeDictionary()
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	torrent := &TorrentFile{
		Announce: dict["announce"].(string),
	}

	infoDict := dict["info"].(map[string]any)
	torrent.Info = InfoDict{
		Length:      infoDict["length"].(int),
		Name:        infoDict["name"].(string),
		PieceLength: infoDict["piece length"].(int),
		Pieces:      []byte(infoDict["pieces"].(string)),
	}

	infoBytes := d.data[d.infoStart:d.infoEnd]
	hash := sha1.Sum(infoBytes)
	torrent.InfoHash = hash

	return torrent, nil
}

func (t *TorrentFile) trackerResponse() (*TrackerResponse, error) {
	// Parse the announce URL
	trackerUrl, err := url.Parse(t.Announce)
	if err != nil {
		return nil, err
	}

	// Add query params
	params := url.Values{}
	params.Add("info_hash", string(t.InfoHash[:])) // This isn't the 40 byte hexadecimal
	params.Add("peer_id", PEER_ID)
	params.Add("port", "6881")
	params.Add("uploaded", "0")
	params.Add("downloaded", "0")
	params.Add("left", fmt.Sprintf("%d", t.Info.Length))
	params.Add("compact", "1")

	trackerUrl.RawQuery = params.Encode()

	resp, err := http.Get(trackerUrl.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	decoder := NewBencodeDecoder(body)
	decodedResp, err := decoder.decodeDictionary()
	if err != nil {
		return nil, err
	}

	trackerResponse := &TrackerResponse{
		Complete:    decodedResp["complete"].(int),
		Incomplete:  decodedResp["incomplete"].(int),
		Interval:    decodedResp["interval"].(int),
		MinInterval: decodedResp["min interval"].(int),
	}

	peersRaw := []byte(decodedResp["peers"].(string))

	idx := 0
	var peers []string
	for idx < len(peersRaw) {
		port := uint16(peersRaw[idx+4])<<8 | uint16(peersRaw[idx+5])
		peer := fmt.Sprintf("%d.%d.%d.%d:%d",
			peersRaw[idx], peersRaw[idx+1], peersRaw[idx+2], peersRaw[idx+3], port)
		peers = append(peers, peer)
		idx += 6
	}
	trackerResponse.Peers = peers

	return trackerResponse, nil
}

func (t *TorrentFile) handShake(peer string) ([]byte, error) {
	var handshake []byte
	handshake = append(handshake, 19)
	handshake = append(handshake, []byte("BitTorrent protocol")...)

	reserved := make([]byte, 8)
	handshake = append(handshake, reserved...)

	handshake = append(handshake, t.InfoHash[:]...)
	handshake = append(handshake, []byte(PEER_ID)...)

	// Connect to peer on TCP
	conn, err := net.Dial("tcp", peer)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Send handshake
	_, err = conn.Write(handshake)
	if err != nil {
		return nil, err
	}

	response := make([]byte, len(handshake))
	_, err = conn.Read(response)
	if err != nil && err != io.EOF {
		return nil, err
	}

	return response[len(handshake)-20 : len(handshake)], nil
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
		fileName := os.Args[2]

		file, err := os.ReadFile(fileName)
		if err != nil {
			fmt.Println(err)
			return
		}

		decoder := NewBencodeDecoder(file)
		torrent, err := decoder.decodeTorrent()
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println("Tracker URL:", torrent.Announce)
		fmt.Println("Length:", torrent.Info.Length)
		fmt.Printf("Info Hash: %x\n", torrent.InfoHash)
		fmt.Println("Piece Length:", torrent.Info.PieceLength)

		fmt.Println("Piece Hashes:")
		pieces := torrent.Info.Pieces
		pieceIdx := 0
		for pieceIdx < len(pieces) {
			fmt.Printf("%x\n", pieces[pieceIdx:pieceIdx+20])
			pieceIdx += 20
		}

	case "peers":
		fileName := os.Args[2]

		file, err := os.ReadFile(fileName)
		if err != nil {
			fmt.Println(err)
			return
		}

		decoder := NewBencodeDecoder(file)
		torrent, err := decoder.decodeTorrent()
		if err != nil {
			fmt.Println(err)
			return
		}

		response, err := torrent.trackerResponse()
		if err != nil {
			fmt.Println(err)
			return
		}

		for _, peer := range response.Peers {
			fmt.Println(peer)
		}

	case "handshake":
		fileName := os.Args[2]
		peer := os.Args[3]

		file, err := os.ReadFile(fileName)
		if err != nil {
			fmt.Println(err)
			return
		}

		decoder := NewBencodeDecoder(file)
		torrent, err := decoder.decodeTorrent()
		if err != nil {
			fmt.Println(err)
			return
		}

		peerID, err := torrent.handShake(peer)
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Printf("Peer ID: %x\n", peerID)

	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
