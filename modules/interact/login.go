// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/auth"
	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/theme"

	"charm.land/huh/v2"
)

// CreateLogin create an login interactive
func CreateLogin(ctx context.Context) error {
	var (
		name, token, user, passwd, otp, scopes, sshKey, sshCertPrincipal, sshKeyFingerprint string
		insecure, sshAgent, versionCheck, helper                                            bool
	)

	versionCheck = true
	helper = false

	giteaURL := "https://gitea.com"
	if err := huh.NewInput().
		Title("URL of Gitea instance: ").
		Value(&giteaURL).
		Validate(func(s string) error {
			s = strings.TrimSpace(s)
			if len(s) == 0 {
				return fmt.Errorf("URL is required")
			}
			_, err := url.Parse(s)
			if err != nil {
				return fmt.Errorf("invalid URL: %v", err)
			}
			return nil
		}).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return err
	}
	printTitleAndContent("URL of Gitea instance: ", giteaURL)

	giteaURL = strings.TrimSuffix(strings.TrimSpace(giteaURL), "/")

	name, err := task.GenerateLoginName(giteaURL, "")
	if err != nil {
		return err
	}

	validateFunc := func(s string) error {
		if err := huh.ValidateNotEmpty()(s); err != nil {
			return err
		}

		logins, err := config.GetLogins()
		if err != nil {
			return err
		}
		for _, login := range logins {
			if login.Name == name {
				return fmt.Errorf("login with name '%s' already exists", name)
			}
		}
		return nil
	}

	if err := huh.NewInput().
		Title("Name of new Login: ").
		Value(&name).
		Validate(validateFunc).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return err
	}

	printTitleAndContent("Name of new Login: ", name)

	loginMethod, err := promptSelectV2("Login with: ", []string{"token", "ssh-key/certificate", "oauth"})
	if err != nil {
		return err
	}
	printTitleAndContent("Login with: ", loginMethod)

	switch loginMethod {
	case "oauth":
		if err := huh.NewConfirm().
			Title("Allow Insecure connections:").
			Value(&insecure).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return err
		}
		printTitleAndContent("Allow Insecure connections:", strconv.FormatBool(insecure))

		return auth.OAuthLoginWithOptions(ctx, name, giteaURL, insecure)
	default: // token
		var hasToken bool
		if err := huh.NewConfirm().
			Title("Do you have an access token?").
			Value(&hasToken).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return err
		}
		printTitleAndContent("Do you have an access token?", strconv.FormatBool(hasToken))

		if hasToken {
			if err := huh.NewInput().
				Title("Token:").
				Value(&token).
				Validate(huh.ValidateNotEmpty()).
				WithTheme(theme.GetTheme()).
				Run(); err != nil {
				return err
			}
			printTitleAndContent("Token:", token)
		} else {
			if err := huh.NewInput().
				Title("Username:").
				Value(&user).
				Validate(huh.ValidateNotEmpty()).
				WithTheme(theme.GetTheme()).
				Run(); err != nil {
				return err
			}
			printTitleAndContent("Username:", user)

			if err := huh.NewInput().
				Title("Password:").
				Value(&passwd).
				Validate(huh.ValidateNotEmpty()).
				EchoMode(huh.EchoModePassword).
				WithTheme(theme.GetTheme()).
				Run(); err != nil {
				return err
			}
			printTitleAndContent("Password:", "********")

			var tokenScopes []string
			if err := huh.NewMultiSelect[string]().
				Title("Token Scopes:").
				Options(huh.NewOptions(tokenScopeOpts...)...).
				Value(&tokenScopes).
				Validate(func(s []string) error {
					if len(s) == 0 {
						return errors.New("at least one scope is required")
					}
					return nil
				}).
				WithTheme(theme.GetTheme()).
				Run(); err != nil {
				return err
			}
			printTitleAndContent("Token Scopes:", strings.Join(tokenScopes, "\n"))

			scopes = strings.Join(tokenScopes, ",")

			// Ask for OTP last so it's less likely to timeout
			if err := huh.NewInput().
				Title("OTP (if applicable):").
				Value(&otp).
				WithTheme(theme.GetTheme()).
				Run(); err != nil {
				return err
			}
			printTitleAndContent("OTP (if applicable):", otp)
		}
	case "ssh-key/certificate":
		if err := huh.NewInput().
			Title("SSH Key/Certificate Path (leave empty for auto-discovery in ~/.ssh and ssh-agent):").
			Value(&sshKey).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return err
		}
		printTitleAndContent("SSH Key/Certificate Path (leave empty for auto-discovery in ~/.ssh and ssh-agent):", sshKey)

		if sshKey == "" {
			pubKeys := task.ListSSHPubkey()
			if len(pubKeys) == 0 {
				fmt.Println("No SSH keys found in ~/.ssh or ssh-agent")
				return nil
			}
			sshKey, err = promptSelect("Select ssh-key: ", pubKeys, "", "", "")
			if err != nil {
				return err
			}
			printTitleAndContent("Selected ssh-key:", sshKey)

			sshKey, sshCertPrincipal, sshKeyFingerprint, sshAgent, err = parseSSHPubkeySelection(sshKey)
			if err != nil {
				return err
			}
		}
	}

	var optSettings bool
	if err := huh.NewConfirm().
		Title("Set Optional settings:").
		Value(&optSettings).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return err
	}
	printTitleAndContent("Set Optional settings:", strconv.FormatBool(optSettings))

	if optSettings {
		if err := huh.NewInput().
			Title("SSH Key Path (leave empty for auto-discovery):").
			Value(&sshKey).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return err
		}
		printTitleAndContent("SSH Key Path (leave empty for auto-discovery):", sshKey)

		if err := huh.NewConfirm().
			Title("Allow Insecure connections:").
			Value(&insecure).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return err
		}
		printTitleAndContent("Allow Insecure connections:", strconv.FormatBool(insecure))

		if err := huh.NewConfirm().
			Title("Add git helper:").
			Value(&helper).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return err
		}
		printTitleAndContent("Add git helper:", strconv.FormatBool(helper))

		if err := huh.NewConfirm().
			Title("Check version of Gitea instance:").
			Value(&versionCheck).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return err
		}
		printTitleAndContent("Check version of Gitea instance:", strconv.FormatBool(versionCheck))
	}

	return task.CreateLogin(ctx, name, token, user, passwd, otp, scopes, sshKey, giteaURL, sshCertPrincipal, sshKeyFingerprint, insecure, sshAgent, versionCheck, helper)
}

func parseSSHPubkeySelection(display string) (sshKey, sshCertPrincipal, sshKeyFingerprint string, sshAgent bool, err error) {
	if strings.Contains(display, "principals") {
		if sshCertPrincipal, err = regexpSubmatch(regexp.MustCompile(`.*?principals: (.*?)[,|\s]`), display); err != nil {
			return "", "", "", false, fmt.Errorf("failed to parse SSH certificate principal from %q: %w", display, err)
		}
		if strings.HasSuffix(display, "(ssh-agent)") {
			return "", sshCertPrincipal, "", true, nil
		}
		if sshKey, err = regexpSubmatch(regexp.MustCompile(`\((.*?)\)$`), display); err != nil {
			return "", "", "", false, fmt.Errorf("failed to parse SSH certificate path from %q: %w", display, err)
		}
		return strings.TrimSuffix(sshKey, "-cert.pub"), sshCertPrincipal, "", false, nil
	}

	if sshKeyFingerprint, err = regexpSubmatch(regexp.MustCompile(`(SHA256:.*?)\s`), display); err != nil {
		return "", "", "", false, fmt.Errorf("failed to parse SSH key fingerprint from %q: %w", display, err)
	}
	if strings.HasSuffix(display, "(ssh-agent)") {
		return "", "", sshKeyFingerprint, true, nil
	}
	if sshKey, err = regexpSubmatch(regexp.MustCompile(`\((.*?)\)$`), display); err != nil {
		return "", "", "", false, fmt.Errorf("failed to parse SSH key path from %q: %w", display, err)
	}
	return strings.TrimSuffix(sshKey, ".pub"), "", sshKeyFingerprint, false, nil
}

func regexpSubmatch(re *regexp.Regexp, s string) (string, error) {
	match := re.FindStringSubmatch(s)
	if len(match) < 2 {
		return "", fmt.Errorf("no match")
	}
	return match[1], nil
}

var tokenScopeOpts = []string{
	string(gitea.AccessTokenScopeAll),
	string(gitea.AccessTokenScopeRepo),
	string(gitea.AccessTokenScopeRepoStatus),
	string(gitea.AccessTokenScopePublicRepo),
	string(gitea.AccessTokenScopeAdminOrg),
	string(gitea.AccessTokenScopeWriteOrg),
	string(gitea.AccessTokenScopeReadOrg),
	string(gitea.AccessTokenScopeAdminPublicKey),
	string(gitea.AccessTokenScopeWritePublicKey),
	string(gitea.AccessTokenScopeReadPublicKey),
	string(gitea.AccessTokenScopeAdminRepoHook),
	string(gitea.AccessTokenScopeWriteRepoHook),
	string(gitea.AccessTokenScopeReadRepoHook),
	string(gitea.AccessTokenScopeAdminOrgHook),
	string(gitea.AccessTokenScopeAdminUserHook),
	string(gitea.AccessTokenScopeNotification),
	string(gitea.AccessTokenScopeUser),
	string(gitea.AccessTokenScopeReadUser),
	string(gitea.AccessTokenScopeUserEmail),
	string(gitea.AccessTokenScopeUserFollow),
	string(gitea.AccessTokenScopeDeleteRepo),
	string(gitea.AccessTokenScopePackage),
	string(gitea.AccessTokenScopeWritePackage),
	string(gitea.AccessTokenScopeReadPackage),
	string(gitea.AccessTokenScopeDeletePackage),
	string(gitea.AccessTokenScopeAdminGPGKey),
	string(gitea.AccessTokenScopeWriteGPGKey),
	string(gitea.AccessTokenScopeReadGPGKey),
	string(gitea.AccessTokenScopeAdminApplication),
	string(gitea.AccessTokenScopeWriteApplication),
	string(gitea.AccessTokenScopeReadApplication),
	string(gitea.AccessTokenScopeSudo),
}
