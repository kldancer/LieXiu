package agentaccess

import "testing"

func TestCanInvoke(t *testing.T) {
	const owner = "owner"
	const member = "member"

	tests := []struct {
		name      string
		principal Principal
		grant     GrantFacts
		targets   []Target
		want      bool
	}{
		{
			name:      "owner always passes private",
			principal: Principal{ActorType: "member", ActorID: owner},
			grant:     GrantFacts{OwnerID: owner, PermissionMode: "private"},
			want:      true,
		},
		{
			name:      "admin-like actor cannot bypass private",
			principal: Principal{ActorType: "member", ActorID: member},
			grant:     GrantFacts{OwnerID: owner, PermissionMode: "private"},
			targets:   []Target{{Type: "workspace"}},
			want:      false,
		},
		{
			name:      "workspace member passes workspace grant",
			principal: Principal{ActorType: "member", ActorID: member},
			grant:     GrantFacts{OwnerID: owner, PermissionMode: "public_to", IsWorkspaceMember: true},
			targets:   []Target{{Type: "workspace"}},
			want:      true,
		},
		{
			name:      "workspace agent passes without originator",
			principal: Principal{ActorType: "agent"},
			grant:     GrantFacts{OwnerID: owner, PermissionMode: "public_to"},
			targets:   []Target{{Type: "workspace"}},
			want:      true,
		},
		{
			name:      "system member grant needs originator",
			principal: Principal{ActorType: "system"},
			grant:     GrantFacts{OwnerID: owner, PermissionMode: "public_to"},
			targets:   []Target{{Type: "member", ID: member}},
			want:      false,
		},
		{
			name:      "agent member grant uses originator",
			principal: Principal{ActorType: "agent", OriginatorUserID: member},
			grant:     GrantFacts{OwnerID: owner, PermissionMode: "public_to"},
			targets:   []Target{{Type: "member", ID: member}},
			want:      true,
		},
		{
			name:      "team and unknown targets are inert",
			principal: Principal{ActorType: "member", ActorID: member},
			grant:     GrantFacts{OwnerID: owner, PermissionMode: "public_to", IsWorkspaceMember: true},
			targets:   []Target{{Type: "team", ID: member}, {Type: "unknown", ID: member}},
			want:      false,
		},
		{
			name:      "unknown permission fails closed",
			principal: Principal{ActorType: "member", ActorID: member},
			grant:     GrantFacts{OwnerID: owner, PermissionMode: "future_mode", IsWorkspaceMember: true},
			targets:   []Target{{Type: "workspace"}},
			want:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CanInvoke(test.principal, test.grant, test.targets); got != test.want {
				t.Fatalf("CanInvoke() = %v, want %v", got, test.want)
			}
		})
	}
}
