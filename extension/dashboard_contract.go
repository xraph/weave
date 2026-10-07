package extension

import (
	"fmt"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"

	weavecontract "github.com/xraph/weave/extension/contract"
)

// RegisterContractContributor registers the weave contract contributor,
// which is what the React dashboard reads. Forge's dashboard discovers it by
// type assertion during Start.
func (e *Extension) RegisterContractContributor(
	disp *dispatcher.Dispatcher,
	reg dashcontract.Registry,
	wreg dashcontract.WardenRegistry,
) error {
	// BaseExtension.Logger returns its logger field as is, so it is a nil
	// interface (not a panic) on an extension that never ran Register.
	logger := e.Logger()
	if e.eng == nil {
		if logger != nil {
			logger.Warn("weave: not initialised; skipping contract contributor registration")
		}
		return nil
	}
	deps := weavecontract.Deps{Engine: e.eng}
	if logger != nil {
		deps.Logger = logger
	}
	if err := weavecontract.Register(disp, reg, wreg, deps); err != nil {
		return fmt.Errorf("weave: register contract contributor: %w", err)
	}
	return nil
}
