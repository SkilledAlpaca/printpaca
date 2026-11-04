package main

import (
	"fmt"
	"strings"
)

// optionSummarySep joins the "Label: Name" parts of an order's stored option_name.
const optionSummarySep = " · "

// optionSelection is one choice in an order: an option of a group, both by their stable ids.
type optionSelection struct {
	GroupID  string `json:"groupId"`
	OptionID string `json:"optionId"`
}

// resolveSelections checks an order's selections against the item's groups and returns the summed price
// delta and the stored option_name ("Color: Red · Size: Large": selected groups only, in display order).
// It returns one message per problem, each prefixed "selections:". Unknown ids, two choices in one group, and
// a required group left unanswered are all problems. Server code only: a client never supplies a delta.
func resolveSelections(groups []ItemOptionGroup, sels []optionSelection) (delta int, summary string, problems []string) {
	chosen := make(map[string]ItemOption, len(sels))
	for _, s := range sels {
		var group *ItemOptionGroup
		for gi := range groups {
			if groups[gi].ID == s.GroupID {
				group = &groups[gi]
				break
			}
		}
		if group == nil {
			problems = append(problems, "selections: unknown option group")
			continue
		}
		if _, dup := chosen[group.ID]; dup {
			problems = append(problems, fmt.Sprintf("selections: choose only one %s", group.Label))
			continue
		}
		found := false
		for _, o := range group.Options {
			if o.ID == s.OptionID {
				chosen[group.ID] = o
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("selections: unknown %s option", group.Label))
		}
	}
	var parts []string
	for _, g := range groups {
		o, ok := chosen[g.ID]
		if !ok {
			if g.Required {
				problems = append(problems, fmt.Sprintf("selections: choose %s", g.Label))
			}
			continue
		}
		delta += o.DeltaCents
		parts = append(parts, g.Label+": "+o.Name)
	}
	if len(problems) > 0 {
		return 0, "", problems
	}
	return delta, strings.Join(parts, optionSummarySep), nil
}

// optionSummaryDelta reports whether s is a valid stored option_name for groups (zero or more "Label: Name"
// parts, one per group at most, in any order) and the price delta of those choices. Invoice lines carry the
// summary string rather than ids, so this parses it back; backtracking keeps labels or names that contain
// ": " or the separator unambiguous.
func optionSummaryDelta(groups []ItemOptionGroup, s string) (delta int, ok bool) {
	if s == "" {
		return 0, true
	}
	used := make([]bool, len(groups))
	var walk func(rest string) (int, bool)
	walk = func(rest string) (int, bool) {
		for gi, g := range groups {
			if used[gi] {
				continue
			}
			for _, o := range g.Options {
				part := g.Label + ": " + o.Name
				switch {
				case rest == part:
					return o.DeltaCents, true
				case strings.HasPrefix(rest, part+optionSummarySep):
					used[gi] = true
					d, ok := walk(rest[len(part)+len(optionSummarySep):])
					used[gi] = false
					if ok {
						return d + o.DeltaCents, true
					}
				}
			}
		}
		return 0, false
	}
	return walk(s)
}
