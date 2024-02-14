// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"fmt"
	"regexp"
	"strings"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/task"

	"github.com/AlecAivazis/survey/v2"
)

// CreateLogin create an login interactive
func CreateLogin() error {
	var (
		name, token, user, passwd, otp, scopes, sshKey, giteaURL, sshCertPrincipal, sshKeyFingerprint string
		insecure, sshAgent, versionCheck                                                              bool
	)

	versionCheck = true

	promptI := &survey.Input{Message: "URL of Gitea instance: "}
	if err := survey.AskOne(promptI, &giteaURL, survey.WithValidator(survey.Required)); err != nil {
		return err
	}
	giteaURL = strings.TrimSuffix(strings.TrimSpace(giteaURL), "/")
	if len(giteaURL) == 0 {
		fmt.Println("URL is required!")
		return nil
	}

	name, err := task.GenerateLoginName(giteaURL, "")
	if err != nil {
		return err
	}

	promptI = &survey.Input{Message: "Name of new Login [" + name + "]: "}
	if err := survey.AskOne(promptI, &name); err != nil {
		return err
	}

	loginMethod, err := promptSelect("Login with: ", []string{"token", "ssh-key/certificate"}, "", "")
	if err != nil {
		return err
	}

	switch loginMethod {
	default: // token
		var hasToken bool
		promptYN := &survey.Confirm{
			Message: "Do you have an access token?",
			Default: false,
		}
		if err = survey.AskOne(promptYN, &hasToken); err != nil {
			return err
		}

		if hasToken {
			promptI = &survey.Input{Message: "Token: "}
			if err := survey.AskOne(promptI, &token, survey.WithValidator(survey.Required)); err != nil {
				return err
			}
		} else {
			promptI = &survey.Input{Message: "Username: "}
			if err = survey.AskOne(promptI, &user, survey.WithValidator(survey.Required)); err != nil {
				return err
			}

			promptPW := &survey.Password{Message: "Password: "}
			if err = survey.AskOne(promptPW, &passwd, survey.WithValidator(survey.Required)); err != nil {
				return err
			}

			var tokenScopes []string
			promptS := &survey.MultiSelect{Message: "Token Scopes:", Options: tokenScopeOpts}
			if err := survey.AskOne(promptS, &tokenScopes, survey.WithValidator(survey.Required)); err != nil {
				return err
			}
			scopes = strings.Join(tokenScopes, ",")

			// Ask for OTP last so it's less likely to timeout
			promptO := &survey.Input{Message: "OTP (if applicable)"}
			if err := survey.AskOne(promptO, &otp); err != nil {
				return err
			}
		}
	case "ssh-key/certificate":
		promptI = &survey.Input{Message: "SSH Key/Certificate Path (leave empty for auto-discovery in ~/.ssh and ssh-agent):"}
		if err := survey.AskOne(promptI, &sshKey); err != nil {
			return err
		}

		if sshKey == "" {
			sshKey, err = promptSelect("Select ssh-key: ", task.ListSSHPubkey(), "", "")
			if err != nil {
				return err
			}

			// ssh certificate
			if strings.Contains(sshKey, "principals") {
				sshCertPrincipal = regexp.MustCompile(`.*?principals: (.*?)[,|\s]`).FindStringSubmatch(sshKey)[1]
				if strings.Contains(sshKey, "(ssh-agent)") {
					sshAgent = true
					sshKey = ""
				} else {
					sshKey = regexp.MustCompile(`\((.*?)\)$`).FindStringSubmatch(sshKey)[1]
					sshKey = strings.TrimSuffix(sshKey, "-cert.pub")
				}
			} else {
				sshKeyFingerprint = regexp.MustCompile(`(SHA256:.*?)\s`).FindStringSubmatch(sshKey)[1]
				if strings.Contains(sshKey, "(ssh-agent)") {
					sshAgent = true
					sshKey = ""
				} else {
					sshKey = regexp.MustCompile(`\((.*?)\)$`).FindStringSubmatch(sshKey)[1]
					sshKey = strings.TrimSuffix(sshKey, ".pub")
				}
			}
		}
	}

	var optSettings bool
	promptYN := &survey.Confirm{
		Message: "Set Optional settings: ",
		Default: false,
	}
	if err = survey.AskOne(promptYN, &optSettings); err != nil {
		return err
	}
	if optSettings {
		promptI = &survey.Input{Message: "SSH Key Path (leave empty for auto-discovery):"}
		if err := survey.AskOne(promptI, &sshKey); err != nil {
			return err
		}

		promptYN = &survey.Confirm{
			Message: "Allow Insecure connections: ",
			Default: false,
		}
		if err = survey.AskOne(promptYN, &insecure); err != nil {
			return err
		}

		promptYN = &survey.Confirm{
			Message: "Check version of Gitea instance: ",
			Default: true,
		}
		if err = survey.AskOne(promptYN, &versionCheck); err != nil {
			return err
		}

	}

	return task.CreateLogin(name, token, user, passwd, otp, scopes, sshKey, giteaURL, sshCertPrincipal, sshKeyFingerprint, insecure, sshAgent, versionCheck)
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
