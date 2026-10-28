module github.com/seaways1015-afk/dbx-plugin-loki

go 1.22

require github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk v0.0.0

// The SDK module is not published under a resolvable tag yet, so a copy of
// t8y2/dbx plugins/sdk/go/dbx-plugin-sdk (Apache-2.0) is vendored here.
replace github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk => ./sdk
