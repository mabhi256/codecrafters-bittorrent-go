package main

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

const PEER_ID = "mabhi12345mabhi12345"

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

type AnnounceResponse struct {
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

type Result struct {
	PieceIndex int
	Data       []byte // nil if failed
	Peer       string
}

type Magnet struct {
	TrackerUrl string   `json:"tracker_url"`
	InfoHash   [20]byte `json:"info_hash"`
	Name       string   `json:"name,omitempty"` // from "dn" parameter if present
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

func (t *TorrentFile) announceTorrent() (*AnnounceResponse, error) {
	return announceRequest(t.Announce, t.InfoHash, t.Info.Length)
}

func announceRequest(trackerUrl string, infoHash [20]byte, length int) (*AnnounceResponse, error) {
	// Parse the announce URL
	tu, err := url.Parse(trackerUrl)
	if err != nil {
		return nil, err
	}

	// Add query params
	params := url.Values{}
	params.Add("info_hash", string(infoHash[:])) // This isn't the 40 byte hexadecimal
	params.Add("peer_id", PEER_ID)
	params.Add("port", "6881")
	params.Add("uploaded", "0")
	params.Add("downloaded", "0")
	params.Add("left", fmt.Sprintf("%d", length))
	params.Add("compact", "1")

	tu.RawQuery = params.Encode()

	resp, err := http.Get(tu.String())
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

	trackerResponse := &AnnounceResponse{
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

func handShake(conn net.Conn, infoHash [20]byte, isExtEnabled bool) ([]byte, bool, error) {
	var handshake []byte
	handshake = append(handshake, 19)
	handshake = append(handshake, []byte("BitTorrent protocol")...)

	reserved := make([]byte, 8)
	if isExtEnabled {
		reserved[5] = 0x10 // ... 0001_0000 0000_0000 0000_0000
	}
	handshake = append(handshake, reserved...)

	handshake = append(handshake, infoHash[:]...)
	handshake = append(handshake, []byte(PEER_ID)...)

	// Send handshake
	_, err := conn.Write(handshake)
	if err != nil {
		return nil, false, err
	}

	response := make([]byte, len(handshake))
	_, err = conn.Read(response)
	if err != nil && err != io.EOF {
		return nil, false, err
	}

	peerReservedBytes := response[20:28]
	isPeerExtEnabled := peerReservedBytes[5]&0x10 != 0

	return response[len(handshake)-20 : len(handshake)], isPeerExtEnabled, nil
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

func (t *TorrentFile) connectPeer(peer string) (net.Conn, error) {
	// Establish a TCP connection with a peer
	conn, err := net.Dial("tcp", peer)
	if err != nil {
		return nil, err
	}

	// Complete a 'BitTorrent protocol' handshake
	_, _, err = handShake(conn, t.InfoHash, false)
	if err != nil {
		return nil, err
	}

	// Wait for a 'bitfield' message
	bitfieldMessage, err := receiveMessage(conn)
	if err != nil {
		return nil, err
	}

	if bitfieldMessage.MessageId != 5 {
		return nil, fmt.Errorf("expecting a 'bitfield' message")
	}

	// Send an 'interested' message
	interestedMessage := &PeerMessage{
		Length:    1,
		MessageId: 2,
		Payload:   []byte{},
	}
	_, err = sendMessage(conn, interestedMessage)
	if err != nil {
		return nil, err
	}

	// Wait for an 'unchoke' message
	unchokeMessage, err := receiveMessage(conn)
	if err != nil {
		return nil, err
	}

	if unchokeMessage.MessageId != 1 {
		return nil, fmt.Errorf("expecting a 'unchoke' message")
	}

	return conn, nil
}

func (t *TorrentFile) downloadPiece(conn net.Conn, pieceIdx int) ([]byte, error) {

	// Send a 'request' message
	// torrent.Info.Pieces is the concatenated [20]byte SHA-1 hash of each piece
	// should be the same as ceiling(fileLength/pieceLength)
	numPieces := len(t.Info.Pieces) / 20

	// Piece size when file size is not divisible by pieceLength
	pieceSize := t.Info.PieceLength
	if pieceIdx == numPieces-1 && t.Info.Length%t.Info.PieceLength != 0 {
		pieceSize = t.Info.Length % t.Info.PieceLength
	}

	blockSize := 1 << 14                                 // 2^14, 16KB
	numBlocks := (pieceSize + blockSize - 1) / blockSize // ceiling

	pieceBytes := make([]byte, pieceSize)

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
		_, err := sendMessage(conn, requestMessage)
		if err != nil {
			return nil, err
		}
	}

	// Wait for each 'piece' message
	for range numBlocks {
		pieceMessage, err := receiveMessage(conn)
		if err != nil {
			return nil, err
		}

		if pieceMessage.MessageId != 7 {
			return nil, fmt.Errorf("expecting a 'unchoke' message")
		}

		_ = binary.BigEndian.Uint32(pieceMessage.Payload[0:4]) // recvIdx
		recvBegin := binary.BigEndian.Uint32(pieceMessage.Payload[4:8])
		block := pieceMessage.Payload[8:]

		copy(pieceBytes[recvBegin:], block)
	}

	// Verify the downloaded piece
	recvHash := sha1.Sum(pieceBytes)
	pieceHash := t.Info.Pieces[pieceIdx*20 : pieceIdx*20+20]
	if recvHash != [20]byte(pieceHash) {
		return nil, fmt.Errorf("expecting: %x,Received: %x", pieceHash, recvHash)
	}

	return pieceBytes, nil
}

func (t *TorrentFile) worker(jobQueue chan int, peerChan chan string, resultChan chan<- Result) {
	conns := make(map[string]net.Conn)
	for pieceIdx := range jobQueue {
		// Try to get ONE peer
		peer := <-peerChan // This blocks until peer available

		conn, exists := conns[peer]
		if !exists {
			var err error
			conn, err = t.connectPeer(peer)
			if err != nil {
				fmt.Printf("Failed to connect to peer %s: %v\n", peer, err)
				peerChan <- peer     // Return peer to pool
				jobQueue <- pieceIdx // Return job to queue
				continue
			}
			conns[peer] = conn
		}

		pieceBytes, err := t.downloadPiece(conn, pieceIdx)
		if err != nil {
			// Close and remove bad connection
			conn.Close()
			delete(conns, peer)

			// FAILED: return peer & job back to their channel
			peerChan <- peer
			jobQueue <- pieceIdx
			continue
		}

		result := Result{
			PieceIndex: pieceIdx,
			Data:       pieceBytes,
			Peer:       peer,
		}

		// SUCCESS: send result, put peer back
		resultChan <- result
		peerChan <- peer
	}

	// Cleanup connections when worker exits
	for _, conn := range conns {
		conn.Close()
	}
}

func ParseMagnet(magnetLink string) (*Magnet, error) {
	ml, err := url.Parse(magnetLink)
	if err != nil {
		return nil, fmt.Errorf("invalid magnet link: %w", err)
	}
	params := ml.Query()

	xt := params.Get("xt")
	if !strings.HasPrefix(xt, "urn:btih:") {
		return nil, fmt.Errorf("invalid xt param")
	}

	trackerURL := params.Get("tr")
	name := params.Get("dn")

	infoHashHex := strings.TrimPrefix(xt, "urn:btih:")
	infoHashBytes, err := hex.DecodeString(infoHashHex)
	if err != nil {
		return nil, err
	}
	var infoHash [20]byte
	copy(infoHash[:], infoHashBytes)

	magnet := &Magnet{
		TrackerUrl: trackerURL,
		InfoHash:   infoHash,
		Name:       name,
	}

	return magnet, nil
}

func (m *Magnet) announceMagnet() (*AnnounceResponse, error) {
	// just use some non-zero value for length,
	// this will make the server think that you still have stuff to download
	return announceRequest(m.TrackerUrl, m.InfoHash, 10)
}

func (m *Magnet) connectPeer(peer string) (net.Conn, string, int, error) {
	// Establish a TCP connection with a peer
	conn, err := net.Dial("tcp", peer)
	if err != nil {
		return nil, "", 0, err
	}

	// Base handshake with peer
	peerID, isPeerExtEnabled, err := handShake(conn, m.InfoHash, true)
	if err != nil {
		return nil, "", 0, err
	}

	// No need to send 'bitfield' message to peer for this challenge
	// Wait for a 'bitfield' message
	bitfieldMessage, err := receiveMessage(conn)
	if err != nil {
		return nil, "", 0, err
	}

	if bitfieldMessage.MessageId != 5 {
		return nil, "", 0, fmt.Errorf("expecting a 'bitfield' message")
	}

	if !isPeerExtEnabled {
		return nil, "", 0, fmt.Errorf("expecting extension enabled peer")
	}

	// Extension handshake with peer (if enabled)
	encodedResp, err := extHandShake(conn)
	if err != nil {
		return nil, "", 0, err
	}

	// skip the handshake extension message id 0
	decoder := NewBencodeDecoder(encodedResp.Payload[1:])
	resp, err := decoder.decodeDictionary()
	if err != nil {
		return nil, "", 0, err
	}

	extId := resp["m"].(map[string]any)["ut_metadata"].(int)

	return conn, fmt.Sprintf("%x", peerID), extId, nil
}

func sendExtensionMsg(conn net.Conn, payload []byte) (*PeerMessage, error) {
	request := &PeerMessage{
		Length:    uint32(len(payload) + 1),
		MessageId: 20, // Extension Message ID
		Payload:   payload,
	}

	_, err := sendMessage(conn, request)
	if err != nil {
		return nil, err
	}

	response, err := receiveMessage(conn)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func extHandShake(conn net.Conn) (*PeerMessage, error) {
	encoder := NewBencodeEncoder()

	extCodes := map[string]any{"ut_metadata": 10, "ut_pex": 22}
	extMsg := map[string]any{"m": extCodes}
	err := encoder.encodeDict(extMsg)
	if err != nil {
		return nil, err
	}

	payload := []byte{0} // extension message id 0
	payload = append(payload, encoder.data...)

	recvExtMessage, err := sendExtensionMsg(conn, payload)
	if err != nil {
		return nil, err
	}

	return recvExtMessage, nil
}

func requestMetadata(conn net.Conn, extId int) (map[string]any, error) {
	encoder := NewBencodeEncoder()

	reqMsg := map[string]any{"msg_type": 0, "piece": 0}
	err := encoder.encodeDict(reqMsg)
	if err != nil {
		return nil, err
	}

	payload := []byte{byte(extId)}
	payload = append(payload, encoder.data...)

	metadataMsg, err := sendExtensionMsg(conn, payload)
	if err != nil {
		return nil, err
	}

	decoder := NewBencodeDecoder(metadataMsg.Payload[1:])
	response, err := decoder.decodeDictionary()
	if err != nil {
		return nil, err
	}

	return response, nil
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

		response, err := torrent.announceTorrent()
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

		peerID, _, err := handShake(conn, torrent.InfoHash, false)
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

		torrent, err := ParseTorrent(fileName)
		if err != nil {
			fmt.Println(err)
			return
		}

		response, err := torrent.announceTorrent()
		if err != nil {
			fmt.Println(err)
			return
		}

		conn, err := torrent.connectPeer(response.Peers[0])
		if err != nil {
			fmt.Println(err)
			return
		}
		defer conn.Close()

		pieceBytes, err := torrent.downloadPiece(conn, pieceIdx)
		if err != nil {
			fmt.Println(err)
			return
		}

		// Create the file
		file, err := os.Create(dest)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer file.Close()

		_, err = file.Write(pieceBytes)
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println("Downloaded and verified pieceIdx:", pieceIdx)

	case "download":
		dest := os.Args[3]
		fileName := os.Args[4]

		torrent, err := ParseTorrent(fileName)
		if err != nil {
			fmt.Println(err)
			return
		}

		response, err := torrent.announceTorrent()
		if err != nil {
			fmt.Println(err)
			return
		}

		// Create job queue - Don't close since we put failed jobs back to the queue
		numPieces := len(torrent.Info.Pieces) / 20
		jobQueue := make(chan int, numPieces)
		for i := range numPieces {
			jobQueue <- i
		}

		// Create result channel to receive downloaded pieces
		resultChan := make(chan Result, numPieces)

		// Peer pool
		peerChan := make(chan string, len(response.Peers))
		for _, peer := range response.Peers {
			peerChan <- peer
		}

		// Start workers which assign the next piece to the next available peer
		numWorkers := min(5, len(response.Peers))
		for range numWorkers {
			go torrent.worker(jobQueue, peerChan, resultChan)
		}

		file, err := os.Create(dest)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer file.Close()

		for a := 1; a <= numPieces; a++ {
			result := <-resultChan

			file.Seek(int64(result.PieceIndex)*int64(torrent.Info.PieceLength), 0)

			_, err = file.Write(result.Data)
			if err != nil {
				fmt.Println(err)
				return
			}
		}

		fmt.Println("Downloaded and verified file")

	case "magnet_parse":
		magnetLink := os.Args[2]
		magnet, err := ParseMagnet(magnetLink)
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Printf("Info Hash: %x\n", magnet.InfoHash)
		fmt.Println("Tracker URL:", magnet.TrackerUrl)

	case "magnet_handshake":
		magnetLink := os.Args[2]
		magnet, err := ParseMagnet(magnetLink)
		if err != nil {
			fmt.Println(err)
			return
		}

		announceResponse, err := magnet.announceMagnet()
		if err != nil {
			fmt.Println(err)
			return
		}

		_, peerId, extId, err := magnet.connectPeer(announceResponse.Peers[0])
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println("Peer Metadata Extension ID:", extId)
		fmt.Printf("Peer ID: %s\n", peerId)

	case "magnet_info":
		magnetLink := os.Args[2]
		magnet, err := ParseMagnet(magnetLink)
		if err != nil {
			fmt.Println(err)
			return
		}

		announceResponse, err := magnet.announceMagnet()
		if err != nil {
			fmt.Println(err)
			return
		}

		conn, _, extId, err := magnet.connectPeer(announceResponse.Peers[0])
		if err != nil {
			fmt.Println(err)
			return
		}

		metadataResp, err := requestMetadata(conn, extId)
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println(metadataResp)

	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
