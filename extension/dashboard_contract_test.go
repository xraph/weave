package extension

import (
	"context"
	"encoding/json"
	"testing"

	dashboard "github.com/xraph/forge/extensions/dashboard"
	dashauth "github.com/xraph/forge/extensions/dashboard/auth"
	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"

	"github.com/xraph/weave"
	"github.com/xraph/weave/collection"
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

func TestWithDashboardTenantReachesTheContract(t *testing.T) {
	var calls []string
	e := New(WithDashboardTenant(func(_ context.Context, p dashcontract.Principal) (string, error) {
		calls = append(calls, p.User.Subject)
		return "t1", nil
	}))
	eng, err := engine.New(engine.WithStore(memory.New()))
	if err != nil {
		t.Fatal(err)
	}
	e.eng = eng
	if err = eng.CreateCollection(weave.WithTenant(context.Background(), "t1"), &collection.Collection{Name: "ours"}); err != nil {
		t.Fatal(err)
	}
	if err = eng.CreateCollection(weave.WithTenant(context.Background(), "t2"), &collection.Collection{Name: "theirs"}); err != nil {
		t.Fatal(err)
	}

	disp := dispatcher.New(nil)
	if err = e.RegisterContractContributor(disp, dashcontract.NewRegistry(), dashcontract.NewWardenRegistry()); err != nil {
		t.Fatalf("register: %v", err)
	}
	data, _, err := disp.Dispatch(context.Background(), dashcontract.Request{
		Envelope: "v1", Kind: dashcontract.KindQuery, Contributor: "weave", Intent: "collections.list", IntentVersion: 1,
		Payload: json.RawMessage(`{}`),
	}, dashcontract.Principal{User: &dashauth.UserInfo{Subject: "alice"}})
	if err != nil {
		t.Fatalf("collections.list: %v", err)
	}
	var out struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Total != 1 {
		t.Errorf("collections.list: %s, %v, want t1's one collection", data, err)
	}
	if len(calls) != 1 || calls[0] != "alice" {
		t.Errorf("resolver calls = %v, want one with alice", calls)
	}
}
