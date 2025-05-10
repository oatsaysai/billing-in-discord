package utils

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

var (
	UserMentionRegex      = regexp.MustCompile(`<@!?(\d+)>`)
	TxIDRegex             = regexp.MustCompile(`\(TxID:\s?(\d+)\)`)
	TxIDsRegex            = regexp.MustCompile(`\(TxIDs:\s?([\d,]+)\)`)
	FirebaseSiteNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,20}[a-z0-9]$`)
	PromptPayRegex        = regexp.MustCompile(`^(\d{10}|\d{13}|ewallet-\d+)$`)
)

// GenerateRandomString generates a random hex string of the given length
func GenerateRandomString(length int) (string, error) {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ExtractMentionIDs extracts user IDs from Discord mention strings
func ExtractMentionIDs(content string) []string {
	var ids []string
	matches := UserMentionRegex.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) > 1 {
			ids = append(ids, match[1])
		}
	}
	return ids
}

// ExtractTxID extracts a transaction ID from a string
func ExtractTxID(content string) (int, bool) {
	match := TxIDRegex.FindStringSubmatch(content)
	if len(match) > 1 {
		id, err := strconv.Atoi(match[1])
		if err == nil {
			return id, true
		}
	}
	return 0, false
}

// ExtractTxIDs extracts multiple transaction IDs from a string
func ExtractTxIDs(content string) ([]int, bool) {
	match := TxIDsRegex.FindStringSubmatch(content)
	if len(match) > 1 {
		idStrs := strings.Split(match[1], ",")
		var ids []int
		for _, idStr := range idStrs {
			id, err := strconv.Atoi(strings.TrimSpace(idStr))
			if err != nil {
				return nil, false
			}
			ids = append(ids, id)
		}
		return ids, true
	}
	return nil, false
}

// FormatNumberWithCommas formats a number with comma separators for thousands
func FormatNumberWithCommas(num float64) string {
	str := strconv.FormatFloat(num, 'f', 2, 64)
	parts := strings.Split(str, ".")
	integerPart := parts[0]
	
	// Format the integer part with commas
	var formatted strings.Builder
	for i, c := range integerPart {
		if i > 0 && (len(integerPart)-i)%3 == 0 {
			formatted.WriteRune(',')
		}
		formatted.WriteRune(c)
	}
	
	// Add decimal part if it exists
	if len(parts) > 1 {
		formatted.WriteRune('.')
		formatted.WriteString(parts[1])
	}
	
	return formatted.String()
}