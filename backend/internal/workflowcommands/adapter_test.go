package workflowcommands

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

func TestAcornOriginalOptionsAndNestedExport(t *testing.T) {
	withMetadata(t, func(scope *cooperative.Scope, m *Metadata) {
		r, e := m.ParseScript(scope, "export const meta={name:'actual',description:'d'}; return await 1;")
		if e != nil || r.Error != "" {
			t.Fatal(r, e)
		}
		for _, s := range []string{"export const meta={name:'a',description:'d'};function f(){export const x=1}", "export const meta={name:'a',description:'d'};if(true){export const x=1}"} {
			r, e := m.ParseScript(scope, s)
			if e != nil || r.Error == "" {
				t.Fatal("nested export admitted", r, e)
			}
		}
	})
}
