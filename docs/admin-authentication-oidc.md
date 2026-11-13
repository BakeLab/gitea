# Pocket ID and OpenID Connect authentication sources

External OAuth2/OpenID Connect authentication sources are managed by Gitea and
stored in its database; they are not declared in `app.ini`. Configure them in
the site administration UI or with `gitea admin auth add-oauth` and
`gitea admin auth update-oauth`.

For Pocket ID at `https://id.bb.pub`, register this exact callback in the OIDC
client:

```text
<ROOT_URL>/user/oauth2/<source-name>/callback
```

For example, with `ROOT_URL=https://git.example.com/` and source name
`pocket-id`, the callback is
`https://git.example.com/user/oauth2/pocket-id/callback`.

Write the client secret to an absolute file readable only by the Gitea account
(mode `0600`). Then create the source with the OIDC discovery URL and scopes:

```sh
sudo -u gitea /usr/local/bin/gitea --config /etc/gitea/app.ini admin auth add-oauth \
  --name pocket-id \
  --provider openidConnect \
  --key <client-id> \
  --secret-file /etc/gitea/pocket-id-client-secret \
  --auto-discover-url https://id.bb.pub/.well-known/openid-configuration \
  --scopes openid --scopes profile --scopes email --scopes groups \
  --group-claim-name groups \
  --admin-group bakeai-admin
```

`bakeai-admin` is the exact Pocket ID group name. Gitea grants site
administrator access only when the verified `groups` claim contains that name;
other Pocket ID users remain ordinary users. The source's `GroupClaimName` and
`AdminGroup` values are serialized into the existing `login_source.cfg` database
field. They do not need separate columns or a schema migration. Keep the
`groups` scope when updating the source.

To allow Pocket ID sign-in without a local registration or password form, first
activate the authentication source, then set:

```ini
[service]
DISABLE_REGISTRATION = false
ALLOW_ONLY_EXTERNAL_REGISTRATION = true
ENABLE_PASSWORD_SIGNIN_FORM = false
SHOW_REGISTRATION_BUTTON = false

[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
```

Gitea creates its internal user record on the first successful Pocket ID login.
`DISABLE_REGISTRATION = true` would also block that first login.

For an existing source, find its ID with `gitea admin auth list` and use
`update-oauth --id <id>` with the same provider options. The ID is required so
an update cannot silently change a different authentication source. Keep the
existing source name when changing its client credentials; it is part of the
callback URL.

`--secret-file` requires an absolute path to a regular, non-symlink file with
no group or other permission bits. The secret must be a single non-empty line;
one trailing newline is accepted. `--secret` remains available for compatibility,
but its value can be visible in process arguments. Gitea stores the configured
secret with the authentication source in its database and backups.
