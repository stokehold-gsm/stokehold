package store

// Which machine a server lives on (0 is this one). Several machines are a Pro
// feature, but the column stays here so that the Free and Pro editions share
// one database. This file sorts after schedules.go, so the migration runs after it.
func init() {
	migrations = append(migrations, `alter table servers add column node_id integer not null default 0;`)
}
