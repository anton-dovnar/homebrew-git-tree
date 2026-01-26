package structs

import (
	"github.com/go-git/go-git/v5/plumbing/object"
	mapset "github.com/deckarep/golang-set/v2"
)

type CommitInfo struct {
	Commit     *object.Commit
	References mapset.Set[string]
}
