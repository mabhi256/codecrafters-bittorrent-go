package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"unicode"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

func decodeBencode(bencodedString string) (any, error) {
	beginDelimiter := bencodedString[0]
	endDelimiter := bencodedString[len(bencodedString)-1]

	switch {
	case unicode.IsDigit(rune(beginDelimiter)):
		return decodeString(bencodedString)

	case string(beginDelimiter) == "i" && string(endDelimiter) == "e":
		return decodeInteger(bencodedString)

	default:
		return "", fmt.Errorf("format not supported")

	}
}

func decodeString(bencodedString string) (any, error) {
	var firstColonIndex int

	for i := 0; i < len(bencodedString); i++ {
		if bencodedString[i] == ':' {
			firstColonIndex = i
			break
		}
	}

	lengthStr := bencodedString[:firstColonIndex]

	length, err := strconv.Atoi(lengthStr)
	if err != nil {
		return "", err
	}

	return bencodedString[firstColonIndex+1 : firstColonIndex+1+length], nil
}

func decodeInteger(bencodedString string) (int, error) {
	if bencodedString == "i-0e" {
		return 0, fmt.Errorf("invalid integer encoding: %s", bencodedString)
	}

	if string(bencodedString[1]) == "0" && string(bencodedString[2]) != "e" {
		return 0, fmt.Errorf("invalid integer encoding: %s", bencodedString)
	}

	var integerStr string
	sign := 1
	if string(bencodedString[1]) == "-" {
		sign = -1
		integerStr = bencodedString[2 : len(bencodedString)-1]
	} else {
		integerStr = bencodedString[1 : len(bencodedString)-1]
	}

	integer, err := strconv.Atoi(integerStr)
	if err != nil {
		return 0, err
	}

	return sign * integer, nil
}

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	// fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	if command == "decode" {
		bencodedValue := os.Args[2]

		decoded, err := decodeBencode(bencodedValue)
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
