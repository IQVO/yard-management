package postgres_test

import (
	"testing"

	"github.com/claudioed/yard-management/internal/adapters/outbound/postgres"
)

func TestMigrationsURLPrefersTheDirectConnection(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"direct url wins", map[string]string{"MIGRATIONS_DATABASE_URL": "direct", "DATABASE_URL": "pooled"}, "direct"},
		{"falls back to the runtime url", map[string]string{"DATABASE_URL": "pooled"}, "pooled"},
		{"empty direct url falls back", map[string]string{"MIGRATIONS_DATABASE_URL": "", "DATABASE_URL": "pooled"}, "pooled"},
		{"nothing set", map[string]string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := postgres.MigrationsURL(env(c.env)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
