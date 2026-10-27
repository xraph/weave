package mongo

import (
	"errors"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// isUniqueViolation reports a duplicate key error (E11000).
func isUniqueViolation(err error) bool {
	return mongo.IsDuplicateKeyError(err) || (err != nil && strings.Contains(err.Error(), "E11000"))
}

// transactionsUnsupported finds, anywhere in err's chain, the error a
// standalone server gives the first operation of a transaction:
// IllegalOperation (20), "Transaction numbers are only allowed on a replica
// set member or mongos".
func transactionsUnsupported(err error) (mongo.CommandError, bool) {
	var cerr mongo.CommandError
	if errors.As(err, &cerr) && cerr.Code == 20 && strings.Contains(cerr.Message, "Transaction numbers are only allowed") {
		return cerr, true
	}
	return mongo.CommandError{}, false
}
