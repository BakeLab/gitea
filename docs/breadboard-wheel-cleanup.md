# Breadboard wheel retention

Create a disabled cleanup rule for the package owner:

```sh
gitea --config /data/gitea/config/app.ini admin configure-breadboard-wheel-cleanup --owner BakeLab
```

Run the command as the Gitea service account. It uses the existing package cleanup
rule model and `cleanup_packages` cron. It retains the newest 10 `breadboard`
package versions by creation time and every version created within 30 days.
The rule matches the full package/version name with `breadboard/.*`.

Review the rule and its preview under the owner's package settings, then enable
it there. On first creation, `--enable` explicitly enables the rule immediately;
deletion occurs on the next cleanup run, including a configured startup run.
The command refuses to replace any existing PyPI cleanup rule for that owner.

Only the `breadboard` PyPI software package is selected. Guest images, board
packs, accelerator packs, asset locks and other package names remain outside
the rule, across all owners. This protects retained asset-lock references
without parsing or guessing their dependencies. Generic Packages are untouched.
Do not broaden the rule to asset packages until cross-owner reference protection
is implemented. Retention operates on whole versions, including every platform
wheel and any other file uploaded to that version. Removed files are reclaimed
later by Gitea's existing unreferenced-blob cleanup.
