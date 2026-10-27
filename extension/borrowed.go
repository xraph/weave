package extension

import "github.com/xraph/weave/store"

// borrowedStore wraps a store built on a *grove.DB the extension did not
// open. Every method passes through to the embedded store, so new methods on
// store.Store need no change here, except Close, which does nothing: the
// database belongs to whoever put it in the container, and other extensions
// are still using it when Weave stops.
type borrowedStore struct {
	store.Store
}

// borrow returns s with a no-op Close.
func borrow(s store.Store) store.Store { return borrowedStore{Store: s} }

// Close leaves the borrowed database open.
func (borrowedStore) Close() error { return nil }
