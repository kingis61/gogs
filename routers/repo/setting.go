// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Unknwon/com"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/modules/setting"
)

const (
	SETTINGS_OPTIONS base.TplName = "repo/settings/options"
	COLLABORATION    base.TplName = "repo/settings/collaboration"
	HOOKS            base.TplName = "repo/settings/hooks"
	HOOK_NEW         base.TplName = "repo/settings/hook_new"
	ORG_HOOK_NEW     base.TplName = "org/settings/hook_new"
)

func Settings(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsOptions"] = true
	ctx.HTML(200, SETTINGS_OPTIONS)
}

func SettingsPost(ctx *middleware.Context, form auth.RepoSettingForm) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsOptions"] = true

	switch ctx.Query("action") {
	case "update":
		if ctx.HasError() {
			ctx.HTML(200, SETTINGS_OPTIONS)
			return
		}

		newRepoName := form.RepoName
		// Check if repository name has been changed.
		if ctx.Repo.Repository.Name != newRepoName {
			isExist, err := models.IsRepositoryExist(ctx.Repo.Owner, newRepoName)
			if err != nil {
				ctx.Handle(500, "IsRepositoryExist", err)
				return
			} else if isExist {
				ctx.Data["Err_RepoName"] = true
				ctx.RenderWithErr(ctx.Tr("form.repo_name_been_taken"), SETTINGS_OPTIONS, nil)
				return
			} else if err = models.ChangeRepositoryName(ctx.Repo.Owner.Name, ctx.Repo.Repository.Name, newRepoName); err != nil {
				if err == models.ErrRepoNameIllegal {
					ctx.Data["Err_RepoName"] = true
					ctx.RenderWithErr(ctx.Tr("form.illegal_repo_name"), SETTINGS_OPTIONS, nil)
				} else {
					ctx.Handle(500, "ChangeRepositoryName", err)
				}
				return
			}
			log.Trace("Repository name changed: %s/%s -> %s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name, newRepoName)
			ctx.Repo.Repository.Name = newRepoName
		}

		br := form.Branch

		if ctx.Repo.GitRepo.IsBranchExist(br) {
			ctx.Repo.Repository.DefaultBranch = br
		}
		ctx.Repo.Repository.Description = form.Description
		ctx.Repo.Repository.Website = form.Website
		ctx.Repo.Repository.IsPrivate = form.Private
		ctx.Repo.Repository.IsGoget = form.GoGet
		if err := models.UpdateRepository(ctx.Repo.Repository); err != nil {
			ctx.Handle(404, "UpdateRepository", err)
			return
		}
		log.Trace("Repository updated: %s/%s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name)

		if ctx.Repo.Repository.IsMirror {
			if form.Interval > 0 {
				ctx.Repo.Mirror.Interval = form.Interval
				ctx.Repo.Mirror.NextUpdate = time.Now().Add(time.Duration(form.Interval) * time.Hour)
				if err := models.UpdateMirror(ctx.Repo.Mirror); err != nil {
					log.Error(4, "UpdateMirror: %v", err)
				}
			}
		}

		ctx.Flash.Success(ctx.Tr("repo.settings.update_settings_success"))
		ctx.Redirect(fmt.Sprintf("/%s/%s/settings", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name))
	case "transfer":
		if ctx.Repo.Repository.Name != form.RepoName {
			ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_repo_name"), SETTINGS_OPTIONS, nil)
			return
		}

		newOwner := ctx.Query("new_owner_name")
		isExist, err := models.IsUserExist(newOwner)
		if err != nil {
			ctx.Handle(500, "IsUserExist", err)
			return
		} else if !isExist {
			ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_owner_name"), SETTINGS_OPTIONS, nil)
			return
		} else if err = models.TransferOwnership(ctx.Repo.Owner, newOwner, ctx.Repo.Repository); err != nil {
			if err == models.ErrRepoAlreadyExist {
				ctx.RenderWithErr(ctx.Tr("repo.settings.new_owner_has_same_repo"), SETTINGS_OPTIONS, nil)
			} else {
				ctx.Handle(500, "TransferOwnership", err)
			}
			return
		}
		log.Trace("Repository transfered: %s/%s -> %s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name, newOwner)
		ctx.Flash.Success(ctx.Tr("repo.settings.transfer_succeed"))
		ctx.Redirect("/")
	case "delete":
		if ctx.Repo.Repository.Name != form.RepoName {
			ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_repo_name"), SETTINGS_OPTIONS, nil)
			return
		}

		if ctx.Repo.Owner.IsOrganization() {
			if !ctx.Repo.Owner.IsOrgOwner(ctx.User.Id) {
				ctx.Error(404)
				return
			}
			if !ctx.User.ValidtePassword(ctx.Query("password")) {
				ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_password"), SETTINGS_OPTIONS, nil)
				return
			}
		} else {
			if !ctx.Repo.Owner.ValidtePassword(ctx.Query("password")) {
				ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_password"), SETTINGS_OPTIONS, nil)
				return
			}
		}

		if err := models.DeleteRepository(ctx.Repo.Owner.Id, ctx.Repo.Repository.Id, ctx.Repo.Owner.Name); err != nil {
			ctx.Handle(500, "DeleteRepository", err)
			return
		}
		log.Trace("Repository deleted: %s/%s", ctx.Repo.Owner.Name, ctx.Repo.Repository.Name)
		if ctx.Repo.Owner.IsOrganization() {
			ctx.Redirect("/org/" + ctx.Repo.Owner.Name + "/dashboard")
		} else {
			ctx.Redirect("/")
		}
	}
}

func SettingsCollaboration(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsCollaboration"] = true

	repoLink := strings.TrimPrefix(ctx.Repo.RepoLink, "/")

	if ctx.Req.Method == "POST" {
		name := strings.ToLower(ctx.Query("collaborator"))
		if len(name) == 0 || ctx.Repo.Owner.LowerName == name {
			ctx.Redirect(ctx.Req.URL.Path)
			return
		}
		has, err := models.HasAccess(name, repoLink, models.WRITABLE)
		if err != nil {
			ctx.Handle(500, "HasAccess", err)
			return
		} else if has {
			ctx.Redirect(ctx.Req.URL.Path)
			return
		}

		u, err := models.GetUserByName(name)
		if err != nil {
			if err == models.ErrUserNotExist {
				ctx.Flash.Error(ctx.Tr("form.user_not_exist"))
				ctx.Redirect(ctx.Req.URL.Path)
			} else {
				ctx.Handle(500, "GetUserByName", err)
			}
			return
		}

		if err = models.AddAccess(&models.Access{UserName: name, RepoName: repoLink,
			Mode: models.WRITABLE}); err != nil {
			ctx.Handle(500, "AddAccess2", err)
			return
		}

		if setting.Service.EnableNotifyMail {
			if err = mailer.SendCollaboratorMail(ctx.Render, u, ctx.User, ctx.Repo.Repository); err != nil {
				ctx.Handle(500, "SendCollaboratorMail", err)
				return
			}
		}

		ctx.Flash.Success(ctx.Tr("repo.settings.add_collaborator_success"))
		ctx.Redirect(ctx.Req.URL.Path)
		return
	}

	// Delete collaborator.
	remove := strings.ToLower(ctx.Query("remove"))
	if len(remove) > 0 && remove != ctx.Repo.Owner.LowerName {
		needDelete := true
		if ctx.User.IsOrganization() {
			// Check if user belongs to a team that has access to this repository.
			auth, err := models.GetHighestAuthorize(ctx.Repo.Owner.Id, ctx.User.Id, ctx.Repo.Repository.Id, 0)
			if err != nil {
				ctx.Handle(500, "GetHighestAuthorize", err)
				return
			}
			if auth > 0 {
				needDelete = false
			}
		}

		if needDelete {
			if err := models.DeleteAccess(&models.Access{UserName: remove, RepoName: repoLink}); err != nil {
				ctx.Handle(500, "DeleteAccess", err)
				return
			}
		}
		ctx.Flash.Success(ctx.Tr("repo.settings.remove_collaborator_success"))
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
		return
	}

	names, err := models.GetCollaboratorNames(repoLink)
	if err != nil {
		ctx.Handle(500, "GetCollaborators", err)
		return
	}

	us := make([]*models.User, len(names))
	for i, name := range names {
		us[i], err = models.GetUserByName(name)
		if err != nil {
			ctx.Handle(500, "GetUserByName", err)
			return
		}
	}

	ctx.Data["Collaborators"] = us
	ctx.HTML(200, COLLABORATION)
}

func Webhooks(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsHooks"] = true

	// Delete web hook.
	remove := com.StrTo(ctx.Query("remove")).MustInt64()
	if remove > 0 {
		if err := models.DeleteWebhook(remove); err != nil {
			ctx.Handle(500, "DeleteWebhook", err)
			return
		}
		ctx.Flash.Success(ctx.Tr("repo.settings.remove_hook_success"))
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/hooks")
		return
	}

	ws, err := models.GetWebhooksByRepoId(ctx.Repo.Repository.Id)
	if err != nil {
		ctx.Handle(500, "GetWebhooksByRepoId", err)
		return
	}

	ctx.Data["Webhooks"] = ws
	ctx.HTML(200, HOOKS)
}

func renderHookTypes(ctx *middleware.Context) {
	ctx.Data["HookTypes"] = []string{"Gogs", "Slack"}
	ctx.Data["HookType"] = "Gogs"
}

func WebHooksNew(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksNew"] = true
	ctx.Data["Webhook"] = models.Webhook{HookEvent: &models.HookEvent{}}
	renderHookTypes(ctx)
	orCtx, err := getOrgRepoCtx(ctx)
	if err != nil {
		ctx.Handle(500, "WebHooksNew(getOrgRepoCtx)", err)
		return
	}

	ctx.HTML(200, orCtx.NewTemplate)
}

func WebHooksNewPost(ctx *middleware.Context, form auth.NewWebhookForm) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksNew"] = true
	ctx.Data["Webhook"] = models.Webhook{HookEvent: &models.HookEvent{}}

	orCtx, err := getOrgRepoCtx(ctx)
	if err != nil {
		ctx.Handle(500, "WebHooksNewPost(getOrgRepoCtx)", err)
		return
	}

	if ctx.HasError() {
		ctx.HTML(200, orCtx.NewTemplate)
		return
	}

	ct := models.JSON
	if form.ContentType == "2" {
		ct = models.FORM
	}

	w := &models.Webhook{
		RepoId:      orCtx.RepoId,
		Url:         form.PayloadUrl,
		ContentType: ct,
		Secret:      form.Secret,
		HookEvent: &models.HookEvent{
			PushOnly: form.PushOnly,
		},
		IsActive:     form.Active,
		HookTaskType: models.GOGS,
		Meta:         "",
		OrgId:        orCtx.OrgId,
	}

	if err := w.UpdateEvent(); err != nil {
		ctx.Handle(500, "UpdateEvent", err)
		return
	} else if err := models.CreateWebhook(w); err != nil {
		ctx.Handle(500, "CreateWebhook", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.add_hook_success"))
	ctx.Redirect(orCtx.Link + "/settings/hooks")
}

func WebHooksEdit(ctx *middleware.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksEdit"] = true

	hookId := com.StrTo(ctx.Params(":id")).MustInt64()
	if hookId == 0 {
		ctx.Handle(404, "setting.WebHooksEdit", nil)
		return
	}

	w, err := models.GetWebhookById(hookId)
	if err != nil {
		if err == models.ErrWebhookNotExist {
			ctx.Handle(404, "GetWebhookById", nil)
		} else {
			ctx.Handle(500, "GetWebhookById", err)
		}
		return
	}

	// set data per HookTaskType
	switch w.HookTaskType {
	case models.SLACK:
		{
			ctx.Data["SlackHook"] = w.GetSlackHook()
			ctx.Data["HookType"] = "Slack"
		}
	default:
		{
			ctx.Data["HookType"] = "Gogs"
		}
	}
	w.GetEvent()
	ctx.Data["Webhook"] = w
	orCtx, err := getOrgRepoCtx(ctx)
	if err != nil {
		ctx.Handle(500, "WebHooksEdit(getOrgRepoCtx)", err)
		return
	}
	ctx.HTML(200, orCtx.NewTemplate)
}

func WebHooksEditPost(ctx *middleware.Context, form auth.NewWebhookForm) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksEdit"] = true

	hookId := com.StrTo(ctx.Params(":id")).MustInt64()
	if hookId == 0 {
		ctx.Handle(404, "setting.WebHooksEditPost", nil)
		return
	}

	w, err := models.GetWebhookById(hookId)
	if err != nil {
		if err == models.ErrWebhookNotExist {
			ctx.Handle(404, "GetWebhookById", nil)
		} else {
			ctx.Handle(500, "GetWebhookById", err)
		}
		return
	}
	w.GetEvent()
	ctx.Data["Webhook"] = w

	orCtx, err := getOrgRepoCtx(ctx)
	if err != nil {
		ctx.Handle(500, "WebHooksEditPost(getOrgRepoCtx)", err)
		return
	}
	if ctx.HasError() {
		ctx.HTML(200, orCtx.NewTemplate)
		return
	}

	ct := models.JSON
	if form.ContentType == "2" {
		ct = models.FORM
	}

	w.Url = form.PayloadUrl
	w.ContentType = ct
	w.Secret = form.Secret
	w.HookEvent = &models.HookEvent{
		PushOnly: form.PushOnly,
	}
	w.IsActive = form.Active
	if err := w.UpdateEvent(); err != nil {
		ctx.Handle(500, "UpdateEvent", err)
		return
	} else if err := models.UpdateWebhook(w); err != nil {
		ctx.Handle(500, "WebHooksEditPost", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.update_hook_success"))
	ctx.Redirect(fmt.Sprintf("%s/settings/hooks/%d", orCtx.Link, hookId))
}

func SlackHooksNewPost(ctx *middleware.Context, form auth.NewSlackHookForm) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksNew"] = true
	ctx.Data["Webhook"] = models.Webhook{HookEvent: &models.HookEvent{}}

	orCtx, err := getOrgRepoCtx(ctx)
	if err != nil {
		ctx.Handle(500, "SlackHooksNewPost(getOrgRepoCtx)", err)
		return
	}

	if ctx.HasError() {
		ctx.HTML(200, orCtx.NewTemplate)
		return
	}

	meta, err := json.Marshal(&models.Slack{
		Domain:  form.Domain,
		Channel: form.Channel,
		Token:   form.Token,
	})
	if err != nil {
		ctx.Handle(500, "SlackHooksNewPost: JSON marshal failed: ", err)
		return
	}

	w := &models.Webhook{
		RepoId:      orCtx.RepoId,
		Url:         models.GetSlackURL(form.Domain, form.Token),
		ContentType: models.JSON,
		Secret:      "",
		HookEvent: &models.HookEvent{
			PushOnly: form.PushOnly,
		},
		IsActive:     form.Active,
		HookTaskType: models.SLACK,
		Meta:         string(meta),
		OrgId:        orCtx.OrgId,
	}
	if err := w.UpdateEvent(); err != nil {
		ctx.Handle(500, "UpdateEvent", err)
		return
	} else if err := models.CreateWebhook(w); err != nil {
		ctx.Handle(500, "CreateWebhook", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.add_hook_success"))
	ctx.Redirect(orCtx.Link + "/settings/hooks")
}

func SlackHooksEditPost(ctx *middleware.Context, form auth.NewSlackHookForm) {
	ctx.Data["Title"] = ctx.Tr("repo.settings")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["PageIsSettingsHooksEdit"] = true

	hookId := com.StrTo(ctx.Params(":id")).MustInt64()
	if hookId == 0 {
		ctx.Handle(404, "SlackHooksEditPost(hookId)", nil)
		return
	}

	orCtx, err := getOrgRepoCtx(ctx)
	if err != nil {
		ctx.Handle(500, "SlackHooksEditPost(getOrgRepoCtx)", err)
		return
	}

	w, err := models.GetWebhookById(hookId)
	if err != nil {
		if err == models.ErrWebhookNotExist {
			ctx.Handle(404, "GetWebhookById", nil)
		} else {
			ctx.Handle(500, "GetWebhookById", err)
		}
		return
	}
	w.GetEvent()
	ctx.Data["Webhook"] = w

	if ctx.HasError() {
		ctx.HTML(200, orCtx.NewTemplate)
		return
	}
	meta, err := json.Marshal(&models.Slack{
		Domain:  form.Domain,
		Channel: form.Channel,
		Token:   form.Token,
	})
	if err != nil {
		ctx.Handle(500, "SlackHooksNewPost: JSON marshal failed: ", err)
		return
	}

	w.Url = models.GetSlackURL(form.Domain, form.Token)
	w.Meta = string(meta)
	w.HookEvent = &models.HookEvent{
		PushOnly: form.PushOnly,
	}
	w.IsActive = form.Active
	if err := w.UpdateEvent(); err != nil {
		ctx.Handle(500, "UpdateEvent", err)
		return
	} else if err := models.UpdateWebhook(w); err != nil {
		ctx.Handle(500, "SlackHooksEditPost", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.update_hook_success"))
	ctx.Redirect(fmt.Sprintf("%s/settings/hooks/%d", orCtx.Link, hookId))
}

type OrgRepoCtx struct {
	OrgId       int64
	RepoId      int64
	Link        string
	NewTemplate base.TplName
}

// determines whether this is a repo context or organization context
func getOrgRepoCtx(ctx *middleware.Context) (*OrgRepoCtx, error) {
	if _, ok := ctx.Data["RepoLink"]; ok {
		return &OrgRepoCtx{
			OrgId:       int64(0),
			RepoId:      ctx.Repo.Repository.Id,
			Link:        ctx.Repo.RepoLink,
			NewTemplate: HOOK_NEW,
		}, nil
	} else if _, ok := ctx.Data["OrgLink"]; ok {
		return &OrgRepoCtx{
			OrgId:       ctx.Org.Organization.Id,
			RepoId:      int64(0),
			Link:        ctx.Org.OrgLink,
			NewTemplate: ORG_HOOK_NEW,
		}, nil
	} else {
		return &OrgRepoCtx{}, errors.New("Unable to set OrgRepo context")
	}
}
