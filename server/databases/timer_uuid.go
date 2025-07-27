package databases

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// UuidToHighLow converts a UUID to high and low 64-bit integers
// for predictable pagination ordering across databases.
func UuidToHighLow(uuid uuid.UUID) (high, low int64) {

	high = int64(binary.BigEndian.Uint64(uuid[0:8]))
	low = int64(binary.BigEndian.Uint64(uuid[8:16]))
	return high, low
}

// HighLowToUuid converts high and low 64-bit integers back to a UUID string
func HighLowToUuid(high, low int64) uuid.UUID {
	bytes := make([]byte, 16)
	binary.BigEndian.PutUint64(bytes[0:8], uint64(high))
	binary.BigEndian.PutUint64(bytes[8:16], uint64(low))
	uuid, _ := uuid.FromBytes(bytes)
	return uuid
}

// GenerateTimerUUID creates a stable UUID from timer namespace and ID for consistent upsert behavior
func GenerateTimerUUID(namespace, timerId string) uuid.UUID {
	// Create a deterministic UUID based on namespace and timer ID
	hash := md5.Sum([]byte(fmt.Sprintf("%s:%s", namespace, timerId)))
	uuid, _ := uuid.FromBytes(hash[:])
	return uuid
}

// FormatExecuteAtWithUuid creates a composite field for DynamoDB pagination
// Format: "2025-01-01T10:00:00.000Z#550e8400-e29b-41d4-a716-446655440000"
func FormatExecuteAtWithUuid(executeAt time.Time, uuidStr string) string {
	return fmt.Sprintf("%s#%s", executeAt.Format("2006-01-02T15:04:05.000Z"), uuidStr)
}

// ParseExecuteAtWithUuid extracts timestamp and UUID from DynamoDB composite field
func ParseExecuteAtWithUuid(composite string) (executeAt time.Time, uuidStr string, err error) {
	// Split on '#' character
	parts := make([]string, 2)
	hashIndex := -1
	for i, char := range composite {
		if char == '#' {
			hashIndex = i
			break
		}
	}

	if hashIndex == -1 {
		return time.Time{}, "", fmt.Errorf("invalid composite format, missing '#' separator: %s", composite)
	}

	parts[0] = composite[:hashIndex]
	parts[1] = composite[hashIndex+1:]

	// Parse timestamp
	executeAt, err = time.Parse("2006-01-02T15:04:05.000Z", parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("failed to parse timestamp '%s': %w", parts[0], err)
	}

	// Validate UUID format
	_, err = uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("failed to parse UUID '%s': %w", parts[1], err)
	}

	return executeAt, parts[1], nil
}
