package mongo

import (
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// isUniqueViolation reports a duplicate key error (E11000).
func isUniqueViolation(err error) bool {
	return mongo.IsDuplicateKeyError(err) || (err != nil && strings.Contains(err.Error(), "E11000"))
}
