package language

import (
	crand "crypto/rand"
	"encoding/binary"
	"hash/fnv"
	"math/rand/v2"
	"sync"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty/function"
)

// Rand is the random source behind the random functions (uuid,
// rand*, fake, the id generators, timerandom, timesequence).
//
// A run has one seed. Each evaluation scope — one attribute of one
// action, one variable default, one provider config attribute — draws
// from its own stream, derived from the seed and the scope's name. So
// a scope's values don't depend on which other scopes ran first
// (parallel runs, map iteration order) or whether other nodes were
// added or removed, and plan and run see the same values for the
// same seed.
type Rand struct {
	*rand.Rand
	fake *gofakeit.Faker
}

// NewRand returns the stream for scope under seed.
func NewRand(seed uint64, scope string) *Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(scope))
	return newRand(rand.NewPCG(seed, h.Sum64()))
}

func newRand(src rand.Source) *Rand {
	return &Rand{Rand: rand.New(src), fake: gofakeit.NewFaker(src, false)}
}

// RandomSeed returns a fresh seed for runs that don't set one.
func RandomSeed() uint64 {
	var b [8]byte
	_, _ = crand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

// Scoped returns a child of parent whose random functions draw from
// the stream for scope. Every expression evaluation in a run goes
// through here so its random values follow the run's seed.
func Scoped(parent *hcl.EvalContext, seed uint64, scope string) *hcl.EvalContext {
	if parent == nil {
		parent = &hcl.EvalContext{Functions: Map()}
	}
	child := parent.NewChild()
	child.Functions = RandomFuncs(NewRand(seed, scope))
	return child
}

// RandomFuncs returns the random functions bound to r.
func RandomFuncs(r *Rand) map[string]function.Function {
	m := map[string]function.Function{}
	for _, d := range registry {
		if d.Random != nil {
			m[d.Name] = d.Random(r)
		}
	}
	return m
}

// unscoped backs the random functions in Map(), for evaluation that
// isn't part of a run (tests, tooling). It's shared, so it locks.
var unscoped = newRand(&lockedSource{src: rand.NewPCG(RandomSeed(), RandomSeed())})

type lockedSource struct {
	mu  sync.Mutex
	src rand.Source
}

func (s *lockedSource) Uint64() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src.Uint64()
}

// fill fills b with random bytes.
func (r *Rand) fill(b []byte) {
	for i := 0; i < len(b); i += 8 {
		var w [8]byte
		binary.LittleEndian.PutUint64(w[:], r.Uint64())
		copy(b[i:], w[:])
	}
}
