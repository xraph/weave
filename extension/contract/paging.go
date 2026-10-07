package contract

const (
	defaultLimit = 25
	maxLimit     = 100
)

// page is embedded in every list input: `limit` and `offset` in, matching
// the stores' offset paging.
type page struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// resolve applies the default and the cap. A negative value is refused: the
// memory store panics on a negative offset and the SQL backends return a
// raw error.
func (p page) resolve() (limit, offset int, err error) {
	if p.Limit < 0 || p.Offset < 0 {
		return 0, 0, badRequest("limit and offset cannot be negative")
	}
	limit = p.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return limit, p.Offset, nil
}

// listOutput is every list's answer: `items` and `total`, plus the limit
// and offset actually applied, so a page can show where it is.
type listOutput[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// emptyIfNil keeps a metadata map from marshalling as null.
func emptyIfNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
