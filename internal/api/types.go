// Package api contains the CupThread API client and the response types it
// consumes. Field names mirror the server's JSON (see
// SaaS/packages/shared/src/schemas.ts, the single source of truth).
package api

import "encoding/json"

// Workspace is a tenant boundary; every developer request is scoped to one.
type Workspace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type WorkspaceMember struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspaceId"`
	ClerkUserID string  `json:"clerkUserId"`
	Role        string  `json:"role"`
	DisplayName *string `json:"displayName"`
	Email       *string `json:"email"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

type Subscription struct {
	ID               string  `json:"id"`
	WorkspaceID      string  `json:"workspaceId"`
	Tier             string  `json:"tier"`
	Status           string  `json:"status"`
	ExtraApps        int     `json:"extraApps"`
	ExtraMembers     int     `json:"extraMembers"`
	CurrentPeriodEnd *string `json:"currentPeriodEnd"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

type MeWorkspaceEntry struct {
	Workspace    Workspace     `json:"workspace"`
	Membership   WorkspaceMember `json:"membership"`
	Subscription *Subscription `json:"subscription"`
}

type MeResponse struct {
	ClerkUserID string              `json:"clerkUserId"`
	Email       *string             `json:"email"`
	Workspaces  []MeWorkspaceEntry  `json:"workspaces"`
}

// ─── Apps ────────────────────────────────────────────────────────────────────

type AppRecord struct {
	AppID               string   `json:"appId"`
	AppKey              string   `json:"appKey"`
	Slug                string   `json:"slug"`
	Name                string   `json:"name"`
	StoreURL            *string  `json:"storeUrl"`
	StoreKind           *string  `json:"storeKind"`
	AppStoreURL         *string  `json:"appStoreUrl"`
	GooglePlayURL       *string  `json:"googlePlayUrl"`
	IconURL             *string  `json:"iconUrl"`
	AllowPublic         bool     `json:"allowPublic"`
	AllowedPlatforms    []string `json:"allowedPlatforms"`
	MaxAttachmentBytes  int      `json:"maxAttachmentBytes"`
	GithubOwner         *string  `json:"githubOwner"`
	GithubRepo          *string  `json:"githubRepo"`
	GithubRepositoryID  *string  `json:"githubRepositoryId"`
	GithubCategoryID    *string  `json:"githubDiscussionCategoryId"`
	GithubCategoryName  *string  `json:"githubDiscussionCategoryName"`
	GithubCategorySlug  *string  `json:"githubDiscussionCategorySlug"`
	GithubSyncEnabled   bool     `json:"githubSyncEnabled"`
	GithubSyncStatus    bool     `json:"githubSyncStatusEnabled"`
	GithubSyncComments  bool     `json:"githubSyncCommentsEnabled"`
	CreatedAt           string   `json:"createdAt"`
	UpdatedAt           string   `json:"updatedAt"`
}

type ListAppsResponse struct {
	Apps  []AppRecord `json:"apps"`
	Total int         `json:"total"`
}

type AppSettings struct {
	AppID                  string `json:"appId"`
	AllowAnonymousRoadmap  bool   `json:"allowAnonymousRoadmap"`
	AllowAnonymousVote     bool   `json:"allowAnonymousVote"`
	AllowAnonymousFeedback bool   `json:"allowAnonymousFeedback"`
	AllowAnonymousChangelog bool  `json:"allowAnonymousChangelog"`
	SDK                    json.RawMessage `json:"sdk"`
	UpdatedAt              string `json:"updatedAt"`
}

type WorkspaceSettingsResponse struct {
	Workspace Workspace            `json:"workspace"`
	Apps      []AppWithSettings    `json:"apps"`
}

type AppWithSettings struct {
	AppID    string       `json:"appId"`
	Name     string       `json:"name"`
	Settings *AppSettings `json:"settings"`
}

// ─── Inbox (feedback submissions) ────────────────────────────────────────────

type SubmissionRecord struct {
	SubmissionID       string  `json:"submissionId"`
	AppID              *string `json:"appId"`
	AppKey             *string `json:"appKey"`
	WorkspaceSlug      string  `json:"workspaceSlug"`
	Title              string  `json:"title"`
	Description        string  `json:"description"`
	ReporterName       *string `json:"reporterName"`
	ReporterEmail      *string `json:"reporterEmail"`
	Platform           string  `json:"platform"`
	AppVersion         *string `json:"appVersion"`
	BuildNumber        *string `json:"buildNumber"`
	Priority           string  `json:"priority"`
	Status             string  `json:"status"`
	GithubDiscussionID *string `json:"githubDiscussionId"`
	GithubDiscussionURL *string `json:"githubDiscussionUrl"`
	GithubError        *string `json:"githubError"`
	CreatedAt          string  `json:"createdAt"`
	UpdatedAt          string  `json:"updatedAt"`
}

type ListSubmissionsResponse struct {
	Submissions []SubmissionRecord `json:"submissions"`
	Total       int                `json:"total"`
}

type RetrySubmissionResponse struct {
	SubmissionID        string  `json:"submissionId"`
	ForwardedToGithub   bool    `json:"forwardedToGithub"`
	GithubDiscussionID  *string `json:"githubDiscussionId"`
	GithubDiscussionURL *string `json:"githubDiscussionUrl"`
	Error               *string `json:"error"`
}

type DeliveryJob struct {
	ID           string  `json:"id"`
	SubmissionID string  `json:"submissionId"`
	WorkspaceID  *string `json:"workspaceId"`
	Status       string  `json:"status"`
	Attempts     int     `json:"attempts"`
	MaxAttempts  int     `json:"maxAttempts"`
	NextAttemptAt string `json:"nextAttemptAt"`
	LastError    *string `json:"lastError"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

type ListDeliveryJobsResponse struct {
	Jobs []DeliveryJob `json:"jobs"`
}

// ─── Feature requests ────────────────────────────────────────────────────────

type AdminFeatureRequest struct {
	ID               string  `json:"id"`
	AppID            string  `json:"appId"`
	Title            string  `json:"title"`
	Description      string  `json:"description"`
	Status           string  `json:"status"`
	ColumnID         *string `json:"columnId"`
	ColumnSlug       *string `json:"columnSlug"`
	ColumnName       *string `json:"columnName"`
	ColumnColor      *string `json:"columnColor"`
	VersionID        *string `json:"versionId"`
	VersionLabel     *string `json:"versionLabel"`
	ReleasedVersion    *string           `json:"releasedVersion"`
	RequesterName      *string           `json:"requesterName"`
	RequesterEmail     *string           `json:"requesterEmail"`
	RequesterAvatarUrl *string           `json:"requesterAvatarUrl"`
	RequesterClerkId   *string           `json:"requesterClerkId"`
	RecentCommenters   []RecentCommenter `json:"recentCommenters"`
	Approved           bool              `json:"approved"`
	ApprovedAt         *string           `json:"approvedAt"`
	CreatedByAdmin   bool    `json:"createdByAdmin"`
	VoteCount        int     `json:"voteCount"`
	RevenueTotal     float64 `json:"revenueTotal"`
	PayingVoters     int     `json:"payingVoters"`
	SubmitterIsPaying bool   `json:"submitterIsPaying"`
	GithubIssueURL   *string `json:"githubIssueUrl"`
	GithubDiscussionURL *string `json:"githubDiscussionUrl"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

type AdminListFeatureRequestsResponse struct {
	Requests []AdminFeatureRequest `json:"requests"`
	Total    int                   `json:"total"`
}

type RecentCommenter struct {
	AuthorName  *string `json:"authorName"`
	ClerkUserID *string `json:"clerkUserId"`
	AvatarURL   *string `json:"avatarUrl"`
}

// ─── Feature request comments ────────────────────────────────────────────────

type FeatureRequestComment struct {
	ID                 string  `json:"id"`
	FeatureRequestID   string  `json:"featureRequestId"`
	AuthorName         *string `json:"authorName"`
	AuthorEmail        *string `json:"authorEmail"`
	AuthorAvatarURL    *string `json:"authorAvatarUrl"`
	Body               string  `json:"body"`
	ParentID           *string `json:"parentId"`
	ReplyToClerkID     *string `json:"replyToClerkId"`
	ReplyToAuthorName  *string `json:"replyToAuthorName"`
	IsHidden           bool    `json:"isHidden"`
	CreatedAt          string  `json:"createdAt"`
}

type ListCommentsResponse struct {
	Comments []FeatureRequestComment `json:"comments"`
}

type CreateCommentInput struct {
	Body              string  `json:"body"`
	AuthorName        *string `json:"authorName,omitempty"`
	AuthorEmail       *string `json:"authorEmail,omitempty"`
	AuthorAvatarURL   *string `json:"authorAvatarUrl,omitempty"`
	ParentID          *string `json:"parentId,omitempty"`
	ReplyToClerkID    *string `json:"replyToClerkId,omitempty"`
	ReplyToAuthorName *string `json:"replyToAuthorName,omitempty"`
}

// ─── Columns & versions ──────────────────────────────────────────────────────

type Column struct {
	ID        string  `json:"id"`
	AppID     string  `json:"appId"`
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	Position  int     `json:"position"`
	IsVisible bool    `json:"isVisible"`
	IsSystem  bool    `json:"isSystem"`
	Kind      string  `json:"kind"`
	Color     string  `json:"color"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

type ListColumnsResponse struct {
	Columns []Column `json:"columns"`
}

type Version struct {
	ID          string  `json:"id"`
	AppID       string  `json:"appId"`
	Label       string  `json:"label"`
	Position    int     `json:"position"`
	Released    bool    `json:"released"`
	ReleasedAt  *string `json:"releasedAt"`
	Description *string `json:"description"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

type ListVersionsResponse struct {
	Versions []Version `json:"versions"`
}

// ─── Changelog ───────────────────────────────────────────────────────────────

type LinkedRequest struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type ChangelogEntry struct {
	ID              string          `json:"id"`
	AppID           string          `json:"appId"`
	Title           string          `json:"title"`
	Body            string          `json:"body"`
	VersionLabel    *string         `json:"versionLabel"`
	VersionID       *string         `json:"versionId"`
	PublishedAt     *string         `json:"publishedAt"`
	ScheduledAt     *string         `json:"scheduledAt"`
	NotifiedAt      *string         `json:"notifiedAt"`
	LinkedRequests  []LinkedRequest `json:"linkedRequests"`
	SubscriberCount int             `json:"subscriberCount"`
	CreatedAt       string          `json:"createdAt"`
	UpdatedAt       string          `json:"updatedAt"`
}

type ListChangelogResponse struct {
	Entries []ChangelogEntry `json:"entries"`
}

// ─── Imports ─────────────────────────────────────────────────────────────────

type ImportJob struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	AppID       string          `json:"appId"`
	Source      string          `json:"source"`
	Mode        string          `json:"mode"`
	Status      string          `json:"status"`
	Options     json.RawMessage `json:"options"`
	Candidates  json.RawMessage `json:"candidates"`
	Stats       json.RawMessage `json:"stats"`
	Attempts    int             `json:"attempts"`
	LastError   *string         `json:"lastError"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

type ListImportJobsResponse struct {
	Jobs []ImportJob `json:"jobs"`
}

type GetImportJobResponse struct {
	Job ImportJob `json:"job"`
}

type CreateImportJobResponse struct {
	Job   ImportJob `json:"job"`
	Drain struct {
		Processed int `json:"processed"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	} `json:"drain"`
}

// ─── Integrations ────────────────────────────────────────────────────────────

type WorkspaceIntegration struct {
	ID               string  `json:"id"`
	WorkspaceID      string  `json:"workspaceId"`
	Provider         string  `json:"provider"`
	AccountID        *string `json:"accountId"`
	AccountLogin     *string `json:"accountLogin"`
	AccountName      *string `json:"accountName"`
	AccountAvatarURL *string `json:"accountAvatarUrl"`
	AccountType      *string `json:"accountType"`
	Scopes           *string `json:"scopes"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

type GitHubIntegrationResponse struct {
	Integration *WorkspaceIntegration `json:"integration"`
}

type ImportIntegrationResponse struct {
	Integration     *WorkspaceIntegration `json:"integration"`
	OAuthConfigured bool                  `json:"oauthConfigured"`
}

type AuthorizeURLResponse struct {
	URL   string `json:"url"`
	State string `json:"state"`
}

type GitHubReposResponse struct {
	Repos []GitHubRepo `json:"repos"`
}

type GitHubRepo struct {
	ID            any    `json:"id"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	FullName      string `json:"fullName"`
	IsPrivate     bool   `json:"isPrivate"`
	HasDiscussions bool  `json:"hasDiscussions"`
	HasIssues     bool   `json:"hasIssues"`
	HTMLURL       string `json:"htmlUrl"`
}

type GitHubCategoriesResponse struct {
	RepositoryID string             `json:"repositoryId"`
	Categories   []GitHubCategory   `json:"categories"`
}

type GitHubCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type ForwardToGitHubResponse struct {
	Success    bool   `json:"success"`
	TargetType string `json:"targetType"`
	ID         string `json:"id"`
	URL        string `json:"url"`
	Number     *int   `json:"number"`
}

type GitHubSyncResponse struct {
	SyncedCount     int      `json:"syncedCount"`
	UpdatedRequests []string `json:"updatedRequests"`
	Errors          []string `json:"errors"`
}

// ─── Notifications ───────────────────────────────────────────────────────────

type Notification struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	Type        string          `json:"type"`
	Title       string          `json:"title"`
	Body        *string         `json:"body"`
	Payload     json.RawMessage `json:"payload"`
	ReadAt      *string         `json:"readAt"`
	CreatedAt   string          `json:"createdAt"`
}

type ListNotificationsResponse struct {
	Notifications []Notification `json:"notifications"`
	Total         int            `json:"total"`
	UnreadCount   int            `json:"unreadCount"`
}

type NotificationPref struct {
	WorkspaceID string   `json:"workspaceId"`
	Channel     string   `json:"channel"`
	EventMask   []string `json:"eventMask"`
	Enabled     bool     `json:"enabled"`
	UpdatedAt   string   `json:"updatedAt"`
}

type ListNotificationPrefsResponse struct {
	Prefs []NotificationPref `json:"prefs"`
}

// ─── Billing ─────────────────────────────────────────────────────────────────

type TierLimits struct {
	Apps                int `json:"apps"`
	SubmissionsPerMonth int `json:"submissionsPerMonth"`
	Members             int `json:"members"`
}

type SubscriptionUsage struct {
	Tier         string `json:"tier"`
	Status       string `json:"status"`
	Limits       TierLimits `json:"limits"`
	ExtraApps    int    `json:"extraApps"`
	ExtraMembers int    `json:"extraMembers"`
	MonthlyPrice float64 `json:"monthlyPrice"`
	Usage        struct {
		Apps                int `json:"apps"`
		SubmissionsThisMonth int `json:"submissionsThisMonth"`
		Members             int `json:"members"`
	} `json:"usage"`
}

type CheckoutSession struct {
	ID              string  `json:"id"`
	WorkspaceID     string  `json:"workspaceId"`
	Tier            string  `json:"tier"`
	Status          string  `json:"status"`
	CheckoutURL     *string `json:"checkoutUrl"`
	PolarConfigured bool    `json:"polarConfigured"`
	Message         *string `json:"message"`
	CreatedAt       string  `json:"createdAt"`
}

type BillingPortalResponse struct {
	PortalURL       *string `json:"portalUrl"`
	PolarConfigured bool    `json:"polarConfigured"`
	Message         *string `json:"message"`
}

// ─── Search ──────────────────────────────────────────────────────────────────

type SearchResultItem struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	Title         string  `json:"title"`
	Snippet       *string `json:"snippet"`
	WorkspaceID   string  `json:"workspaceId"`
	WorkspaceName string  `json:"workspaceName"`
	AppID         *string `json:"appId"`
	AppName       *string `json:"appName"`
	Status        *string `json:"status"`
}

type SearchResponse struct {
	Query   string             `json:"query"`
	Results []SearchResultItem `json:"results"`
	Total   int                `json:"total"`
}

// ─── Workspaces: members & invitations ───────────────────────────────────────

type ListMembersResponse struct {
	Members []WorkspaceMember `json:"members"`
	Total   int               `json:"total"`
}

type Invitation struct {
	ID                 string `json:"id"`
	WorkspaceID        string `json:"workspaceId"`
	Email              string `json:"email"`
	Role               string `json:"role"`
	InvitedByClerkUserID string `json:"invitedByClerkUserId"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

type ListInvitationsResponse struct {
	Invitations []Invitation `json:"invitations"`
	Total       int          `json:"total"`
}

type AddMemberResponse struct {
	Member     *WorkspaceMember `json:"member"`
	Invitation *Invitation      `json:"invitation"`
	Status     string           `json:"status"`
}

type CreateWorkspaceResponse struct {
	Workspace  Workspace       `json:"workspace"`
	Membership WorkspaceMember `json:"membership"`
}

// ─── Public user profiles ────────────────────────────────────────────────────

type UserProfile struct {
	ClerkUserID  string  `json:"clerkUserId"`
	DisplayName  *string `json:"displayName"`
	AvatarURL    *string `json:"avatarUrl"`
	Bio          *string `json:"bio"`
	WebsiteURL   *string `json:"websiteUrl"`
	HideComments bool    `json:"hideComments"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

type PublicAppSummary struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	IconURL      *string `json:"iconUrl"`
	Description  *string `json:"description"`
	RequestCount int     `json:"requestCount"`
}

type UserProfileComment struct {
	ID                  string `json:"id"`
	Body                string `json:"body"`
	CreatedAt           string `json:"createdAt"`
	FeatureRequestID    string `json:"featureRequestId"`
	FeatureRequestTitle string `json:"featureRequestTitle"`
	AppID               string `json:"appId"`
	AppName             string `json:"appName"`
}

type PublicUserProfileResponse struct {
	Profile        UserProfile          `json:"profile"`
	Apps           []PublicAppSummary   `json:"apps"`
	RecentComments []UserProfileComment `json:"recentComments"`
	HideComments   bool                 `json:"hideComments"`
}

// ─── Generic envelopes ───────────────────────────────────────────────────────

type UploadImageResponse struct {
	URL string `json:"url"`
}
