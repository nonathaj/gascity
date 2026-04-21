package main

import (
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/sling"
)

func collectAttachedBeads(parent beads.Bead, store beads.Store, childQuerier BeadChildQuerier) ([]beads.Bead, error) {
	return sling.CollectAttachedBeads(parent, store, childQuerier)
}

func attachmentLabel(b beads.Bead) string {
	return sling.AttachmentLabel(b)
}

func isAttachedRoot(b beads.Bead) bool {
	return sling.IsAttachedRoot(b)
}

func isWorkflowAttachment(b beads.Bead) bool {
	return sling.IsWorkflowAttachment(b)
}

func isMoleculeAttachment(b beads.Bead) bool {
	return sling.IsMoleculeAttachment(b)
}
