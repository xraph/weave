package extension

import (
	"testing"

	dashboard "github.com/xraph/forge/extensions/dashboard"
	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"

	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/store/memory"
)

// The dashboard finds contract contributors by type assertion at runtime,
// so production code never imports forge's dashboard root. This keeps the
// method checked against the real interface anyway.
var _ dashboard.ContractContributorAware = (*Extension)(nil)

func TestRegisterContractContributor(t *testing.T) {
	e := New()
	eng, err := engine.New(engine.WithStore(memory.New()))
	if err != nil {
		t.Fatal(err)
	}
	e.eng = eng

	reg := dashcontract.NewRegistry()
	if err := e.RegisterContractContributor(dispatcher.New(nil), reg, dashcontract.NewWardenRegistry()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, ok := reg.Contributor("weave"); !ok {
		t.Fatal("weave contributor not registered")
	}
}

// Before Register runs there is no engine; the dashboard's discovery must
// not fail the whole app over it.
func TestRegisterContractContributor_NoEngine(t *testing.T) {
	reg := dashcontract.NewRegistry()
	if err := New().RegisterContractContributor(dispatcher.New(nil), reg, dashcontract.NewWardenRegistry()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, ok := reg.Contributor("weave"); ok {
		t.Fatal("registered without an engine")
	}
}
