package common

import (
	"github.com/rigfile/rigfile/internal/capture"
	"github.com/rigfile/rigfile/internal/splice"
)

// CaptureMarkdownRegions adds the user's own text (outside Rigfile's marked regions) as one instruction and, with
// IncludeManaged, every marked region as an instruction of its own id.
func CaptureMarkdownRegions(b *capture.Builder, doc []byte, from, ownID string) {
	body, ids, err := splice.StripAll(doc, splice.HTML)
	if err != nil {
		b.Add("skipped", "instruction", from, "its Rigfile markers are malformed; fix or remove them and re-run ("+err.Error()+")")
		return
	}
	if len(ids) > 0 && !b.O.IncludeManaged {
		b.Add("note", "instruction", from, "sections managed by Rigfile were left out")
	}
	b.Instruction(ownID, body, from)
	if b.O.IncludeManaged {
		regs, _ := splice.Regions(doc, splice.HTML)
		for _, r := range regs {
			b.Instruction(r.ID, r.Body, from+" (managed section)")
		}
	}
}
