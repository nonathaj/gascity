package main

import "github.com/gastownhall/gascity/internal/config"

// newDemandClaim is one template's claim on the capacity left after the
// resume tier, in config order.
type newDemandClaim struct {
	agent    *config.Agent
	template string
	// demand is the template's effective new demand for this tick.
	demand int
	// concrete counts protected and in-flight sessions: capacity that was
	// already spent on this template and is still coming up.
	concrete int
}

// fairNewDemandShares decides how many new-tier sessions each claim may
// materialize, given the usage already accepted for the resume tier.
//
// Concrete capacity is kept first: those sessions exist, and dropping one to
// fund another template's fresh create would churn a session that is already
// starting. Fresh creates then water-fill: every slot goes to the template
// currently holding the fewest sessions, config order breaking ties, so a
// template at zero sessions is granted its first slot before any template is
// granted another. Greedy config order instead let a template listed early
// with a standing backlog take every slot the resume tier freed, starving the
// templates listed after it for as long as that backlog lasted.
//
// Uncontended capacity yields exactly each template's capped demand; only
// shared workspace or rig caps change who is served. The shares always fit the
// caps, so materializing them never trips a cap rejection downstream.
func fairNewDemandShares(limits nestedCapLimits, usage nestedCapUsage, claims []newDemandClaim) []int {
	shares := make([]int, len(claims))
	scratch := usage.countsOnly()
	for i, claim := range claims {
		shares[i] = capNewDemandCount(limits, scratch, claim.agent, minInt(claim.concrete, claim.demand))
		scratch.acceptCount(claim.template, shares[i], limits)
	}
	for {
		next, eligible := -1, 0
		for i, claim := range claims {
			if shares[i] >= claim.demand || capNewDemandCount(limits, scratch, claim.agent, 1) == 0 {
				continue
			}
			eligible++
			if next < 0 || scratch.agentCount[claim.template] < scratch.agentCount[claims[next].template] {
				next = i
			}
		}
		if next < 0 {
			return shares
		}
		grant := 1
		if eligible == 1 {
			// Nothing left to share with: the last contender takes what its caps allow.
			grant = capNewDemandCount(limits, scratch, claims[next].agent, claims[next].demand-shares[next])
		}
		shares[next] += grant
		scratch.acceptCount(claims[next].template, grant, limits)
	}
}

// countsOnly copies u's cap counters, without its request history, for
// simulating grants.
func (u nestedCapUsage) countsOnly() nestedCapUsage {
	c := newNestedCapUsage()
	for template, n := range u.agentCount {
		c.agentCount[template] = n
	}
	for rig, n := range u.rigCount {
		c.rigCount[rig] = n
	}
	c.workspaceCount = u.workspaceCount
	return c
}

// acceptCount charges n sessions of template against every cap scope, like n
// calls to accept without recording requests.
func (u *nestedCapUsage) acceptCount(template string, n int, limits nestedCapLimits) {
	u.agentCount[template] += n
	if rig := limits.agentRig[template]; rig != "" {
		u.rigCount[rig] += n
	}
	u.workspaceCount += n
}

// withoutNewRequestsFor returns the usage every other claim put on capacity:
// u minus template's own new-tier requests. Resume-tier requests stay, since
// the new tier never displaces them.
func (u nestedCapUsage) withoutNewRequestsFor(template string, limits nestedCapLimits) nestedCapUsage {
	view := u.countsOnly()
	view.requests = make([]SessionRequest, 0, len(u.requests))
	for _, req := range u.requests {
		if req.Template == template && req.Tier == "new" {
			view.acceptCount(template, -1, limits)
			continue
		}
		view.requests = append(view.requests, req)
	}
	return view
}
