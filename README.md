# vintagestory-go

Reusable Go clients and data utilities for Vintage Story services. This module contains no launcher UI, storage, credential-store, or database integration.

Packages:

- `auth`: login, session validation, and TOTP challenges for the Vintage Story authentication API.
- `versions`: official game-release catalog retrieval and validation.
- `moddb`: Vintage Story ModDB catalog, search, details, releases, and tags.
- `modpack`: catalog-agnostic installed-mod update analysis.
- `servers`: public Vintage Story server catalog discovery.
- `news`: official news feed retrieval and normalization.

```go
authClient := auth.NewClient(nil)
session, challenge, err := authClient.Login(ctx, email, password, "", "")

catalog := moddb.NewClient(nil)
mods, err := catalog.Search(ctx, moddb.SearchOptions{Text: "storage", Page: 1})
```

```go
releases, err := versions.NewCatalog(nil).List(ctx)
```

```go
client := servers.NewClient(nil)
list, err := client.List(ctx)
for _, server := range list {
	fmt.Println(server.Name, server.Address)
}

posts, err := news.NewClient(nil, "MyApp/1.0").List(ctx)
```

Licensed under GPL-3.0-only, compatible with the extracted source.
