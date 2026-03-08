package lifecycle

// Transition describes a valid state transition and what triggers it.
type Transition[S ~string] struct {
	From    S
	To      S
	Trigger string
}

type transitionKey struct{ from, to string }

func buildTransitionSet[S ~string](ts []Transition[S]) map[transitionKey]bool {
	m := make(map[transitionKey]bool, len(ts))
	for _, t := range ts {
		m[transitionKey{string(t.From), string(t.To)}] = true
	}
	return m
}
