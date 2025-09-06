package main

import (
	"crypto/sha1"
	"encoding/binary"
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

type PeerMessage struct {
	Length    uint32
	MessageId uint8
	Payload   []byte
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

func handShake(conn net.Conn, infoHash [20]byte) ([]byte, error) {
	var handshake []byte
	handshake = append(handshake, 19)
	handshake = append(handshake, []byte("BitTorrent protocol")...)

	reserved := make([]byte, 8)
	handshake = append(handshake, reserved...)

	handshake = append(handshake, infoHash[:]...)
	handshake = append(handshake, []byte(PEER_ID)...)

	// Send handshake
	_, err := conn.Write(handshake)
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

func receiveMessage(conn net.Conn) (*PeerMessage, error) {
	lengthBytes := make([]byte, 4)
	_, err := io.ReadFull(conn, lengthBytes)
	if err != nil && err != io.EOF {
		return nil, err
	}

	length := binary.BigEndian.Uint32(lengthBytes)
	response := make([]byte, length)
	_, err = io.ReadFull(conn, response)
	if err != nil && err != io.EOF {
		return nil, err
	}

	messageId := response[0]
	payload := response[1:]

	peerMessage := &PeerMessage{
		Length:    length,
		MessageId: messageId,
		Payload:   payload,
	}

	return peerMessage, nil
}

func sendMessage(conn net.Conn, message *PeerMessage) (int, error) {
	var messageBytes []byte
	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, message.Length)
	messageBytes = append(messageBytes, lengthBytes...)

	messageBytes = append(messageBytes, message.MessageId)

	messageBytes = append(messageBytes, message.Payload...)

	n, err := conn.Write(messageBytes)
	if err != nil {
		return n, err
	}

	return n, nil
}

func ParseTorrent(fileName string) (*TorrentFile, error) {
	file, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}

	decoder := NewBencodeDecoder(file)
	torrent, err := decoder.decodeTorrent()
	if err != nil {
		return nil, err
	}

	return torrent, nil
}

func downloadPiece(dest, fileName string, pieceIdx int) {
	torrent, err := ParseTorrent(fileName)
	if err != nil {
		fmt.Println(err)
		return
	}

	response, err := torrent.trackerResponse()
	if err != nil {
		fmt.Println(err)
		return
	}

	// 0. Establish a TCP connection with a peer
	conn, err := net.Dial("tcp", response.Peers[1])
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()

	// 1. Complete a 'BitTorrent protocol' handshake
	_, err = handShake(conn, torrent.InfoHash)
	if err != nil {
		fmt.Println(err)
		return
	}

	// 2. Wait for a 'bitfield' message
	bitfieldMessage, err := receiveMessage(conn)
	if err != nil {
		fmt.Println(err)
		return
	}

	if bitfieldMessage.MessageId != 5 {
		fmt.Println("Expecting a 'bitfield' message")
		return
	}

	// 3. Send an 'interested' message
	interestedMessage := &PeerMessage{
		Length:    1,
		MessageId: 2,
		Payload:   []byte{},
	}
	_, err = sendMessage(conn, interestedMessage)
	if err != nil {
		fmt.Println(err)
		return
	}

	// 4. Wait for an 'unchoke' message
	unchokeMessage, err := receiveMessage(conn)
	if err != nil {
		fmt.Println(err)
		return
	}

	if unchokeMessage.MessageId != 1 {
		fmt.Println("Expecting a 'unchoke' message")
		return
	}

	// 5. Send a 'request' message
	// torrent.Info.Pieces is the concatenated [20]byte SHA-1 hash of each piece
	// should be the same as ceiling(fileLength/pieceLength)
	numPieces := len(torrent.Info.Pieces) / 20

	// Piece size when file size is not divisible by pieceLength
	pieceSize := torrent.Info.PieceLength
	if pieceIdx == numPieces-1 && torrent.Info.Length%torrent.Info.PieceLength != 0 {
		pieceSize = torrent.Info.Length % torrent.Info.PieceLength
	}

	blockSize := 1 << 14                                 // 2^14, 16KB
	numBlocks := (pieceSize + blockSize - 1) / blockSize // ceiling

	// Request each block
	for blockIdx := range numBlocks {
		begin := blockIdx * blockSize
		length := blockSize

		// Handle last block of the last piece
		if blockIdx == numBlocks-1 {
			length = pieceSize - begin
		}

		payload := make([]byte, 12)
		binary.BigEndian.PutUint32(payload[0:4], uint32(pieceIdx))
		binary.BigEndian.PutUint32(payload[4:8], uint32(begin))
		binary.BigEndian.PutUint32(payload[8:12], uint32(length))

		requestMessage := &PeerMessage{
			Length:    13, // 1 byte message ID + 12 bytes payload
			MessageId: 6,
			Payload:   payload,
		}
		_, err = sendMessage(conn, requestMessage)
		if err != nil {
			fmt.Println(err)
			return
		}
	}

	// 6. Create the file
	file, err := os.Create(dest)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()

	// Pre-allocate the file to the desired size (fills with zeros)
	err = file.Truncate(int64(pieceSize))
	if err != nil {
		fmt.Printf("Error truncating file: %v\n", err)
		return
	}

	// 7. Wait for each 'piece' message
	for range numBlocks {
		pieceMessage, err := receiveMessage(conn)
		if err != nil {
			fmt.Println(err)
			return
		}

		if pieceMessage.MessageId != 7 {
			fmt.Println("Expecting a 'unchoke' message")
			return
		}

		_ = binary.BigEndian.Uint32(pieceMessage.Payload[0:4]) // recvIdx
		recvBegin := binary.BigEndian.Uint32(pieceMessage.Payload[4:8])
		block := pieceMessage.Payload[8:]

		_, err = file.Seek(int64(recvBegin), 0)
		if err != nil {
			fmt.Println(err)
			return
		}

		_, err = file.Write(block)
		if err != nil {
			fmt.Println(err)
			return
		}
	}

	fileContent, err := os.ReadFile(dest)
	if err != nil {
		fmt.Println(err)
		return
	}
	recvHash := sha1.Sum(fileContent)
	pieceHash := torrent.Info.Pieces[pieceIdx*20 : pieceIdx*20+20]

	if recvHash != [20]byte(pieceHash) {
		fmt.Printf("Expecting: %x,\nReceived: %x\n", pieceHash, recvHash)
		fmt.Println("Available:")
		pieces := torrent.Info.Pieces
		idx := 0
		for idx < len(pieces) {
			fmt.Printf("%x\n", pieces[idx:idx+20])
			idx += 20
		}
		return
	} else {
		fmt.Println("Downloaded and verified pieceIdx:", pieceIdx)
	}
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
		torrent, err := ParseTorrent(fileName)
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
		torrent, err := ParseTorrent(fileName)
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
		torrent, err := ParseTorrent(fileName)
		if err != nil {
			fmt.Println(err)
			return
		}
		peer := os.Args[3]

		// Connect to peer on TCP
		conn, err := net.Dial("tcp", peer)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer conn.Close()

		peerID, err := handShake(conn, torrent.InfoHash)
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Printf("Peer ID: %x\n", peerID)

	case "download_piece":
		dest := os.Args[3]
		fileName := os.Args[4]
		pieceIdx, err := strconv.Atoi(os.Args[5])
		if err != nil {
			fmt.Println(err)
			return
		}

		downloadPiece(dest, fileName, pieceIdx)

	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
