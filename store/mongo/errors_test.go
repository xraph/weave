package mongo

import (
	"errors"
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// TestTransactionsUnsupported pins the classifier against the error a
// standalone MongoDB 9 returns for the first read in a transaction, wrapped
// the way grove and the store wrap it.
func TestTransactionsUnsupported(t *testing.T) {
	standalone := mongo.CommandError{
		Code:    20,
		Name:    "IllegalOperation",
		Message: "Transaction numbers are only allowed on a replica set member or mongos",
	}
	wrapped := fmt.Errorf("weave: replace source document: %w", fmt.Errorf("mongodriver: find one: %w", standalone))
	if got, ok := transactionsUnsupported(wrapped); !ok || got.Code != 20 {
		t.Errorf("standalone error: got %v, %v; want the command error", got, ok)
	}

	for _, err := range []error{
		nil,
		errors.New("Transaction numbers are only allowed on a replica set member or mongos"),
		mongo.CommandError{Code: 20, Name: "IllegalOperation", Message: "something else"},
		mongo.CommandError{Code: 112, Name: "WriteConflict", Message: "write conflict"},
	} {
		if _, ok := transactionsUnsupported(err); ok {
			t.Errorf("%v: classified as transactions unsupported", err)
		}
	}
}
