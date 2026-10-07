package memory_test

import (
	"testing"

	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/memory"
	"github.com/xraph/weave/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return memory.New() })
}
