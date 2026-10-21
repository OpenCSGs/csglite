import { signal } from "@preact/signals";
import { useEffect, useMemo, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import {
  createTeam,
  createTeamUser,
  deleteTeam,
  deleteTeamUser,
  getTeams,
  getTeamSettings,
  getTeamUsers,
  inviteLink,
  isApiError,
  isFeatureNotLicensedError,
  login,
  reinviteTeamUser,
  SETUP_TOKEN_REQUIRED,
  TEAM_MANAGEMENT_FEATURE,
  updateTeam,
  updateTeamSettings,
  updateTeamUser,
} from "../api/client";
import type { TeamGroup, TeamRole, TeamSettings, TeamUser } from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { authSession, isAdmin, loadAuthSession, seesEverything, userLabel } from "../auth";
import { locale, t } from "../i18n";
import { formatLicenseDate, licenseState } from "../license";

const roles: TeamRole[] = ["admin", "auditor", "member"];
type Tab = "members" | "teams" | "invites" | "security";

const users = signal<TeamUser[]>([]);
const teams = signal<TeamGroup[]>([]);
const settings = signal<TeamSettings | null>(null);
const maxUsers = signal(0);
const loading = signal(false);
const loadError = signal("");
const activeTab = signal<Tab>("members");
/** A freshly issued invitation link, shown until dismissed. */
const issuedInvite = signal<{ user: TeamUser; token: string } | null>(null);

async function loadTeamData() {
  loading.value = true;
  loadError.value = "";
  try {
    const [userList, teamList, signIn] = await Promise.all([getTeamUsers(), getTeams(), getTeamSettings()]);
    users.value = userList.users;
    maxUsers.value = userList.max_users;
    teams.value = teamList;
    settings.value = signIn;
  } catch (err: any) {
    loadError.value = err?.message || t("team.loadFailed");
  } finally {
    loading.value = false;
  }
}

function errorText(err: any, fallback: string): string {
  if (isFeatureNotLicensedError(err) && err.limit) return t("team.seatLimitReached", err.limit);
  return err?.message || fallback;
}

function formatDate(iso?: string): string {
  return iso ? formatLicenseDate(iso, locale.value) : "";
}

const inputClass =
  "mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500";
const selectClass =
  "rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-700 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500";
const primaryButton =
  "rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-60";
const secondaryButton =
  "rounded-lg border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60";
const linkButton = "px-1.5 text-sm text-indigo-600 hover:underline disabled:opacity-50";

export function Team() {
  void locale.value;
  const session = authSession.value;
  const license = licenseState.value;
  const licensed = (license?.features ?? []).includes(TEAM_MANAGEMENT_FEATURE);
  const accountsOn = !!session?.accounts_enabled;
  const canRead = seesEverything(session);

  useEffect(() => {
    if (licensed && accountsOn && canRead) void loadTeamData();
  }, [licensed, accountsOn, canRead]);

  let body;
  if (!canRead) {
    body = <Notice tone="neutral" title={t("team.adminOnlyTitle")} text={t("team.adminOnlyDesc")} />;
  } else if (!licensed && !accountsOn) {
    body = <FeatureLocked />;
  } else if (!licensed) {
    body = <Notice tone="warn" title={t("team.licenseLapsedTitle")} text={t("team.licenseLapsedDesc")} />;
  } else if (!accountsOn) {
    body = <EnableAccounts />;
  } else {
    body = <TeamManager />;
  }

  return (
    <div class="page-shell space-y-6">
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.28em] text-indigo-500">{t("team.eyebrow")}</p>
          <h1 class="mt-2 text-3xl font-bold tracking-tight text-gray-950">{t("team.title")}</h1>
          <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-600">{t("team.subtitle")}</p>
        </div>
        {licensed && accountsOn && canRead && <HeaderStats />}
      </div>
      {body}
    </div>
  );
}

function HeaderStats() {
  const pending = users.value.filter((user) => user.pending).length;
  const seats = maxUsers.value;
  const stats = [
    { label: t("team.statMembers"), value: seats > 0 ? `${users.value.length} / ${seats}` : String(users.value.length) },
    { label: t("team.statPending"), value: String(pending) },
    { label: t("team.statTeams"), value: String(teams.value.length) },
  ];
  return (
    <div class="flex gap-3">
      {stats.map((stat) => (
        <div key={stat.label} class="min-w-[96px] rounded-xl border border-gray-200 bg-white px-4 py-2.5 shadow-sm">
          <p class="text-xs text-gray-500">{stat.label}</p>
          <p class="mt-0.5 text-lg font-semibold text-gray-900">{stat.value}</p>
        </div>
      ))}
    </div>
  );
}

function Notice({ tone, title, text }: { tone: "neutral" | "warn"; title: string; text: string }) {
  const classes = tone === "warn" ? "border-amber-200 bg-amber-50 text-amber-900" : "border-gray-200 bg-white text-gray-700";
  return (
    <section class={`rounded-xl border p-5 text-sm ${classes}`}>
      <p class="font-semibold">{title}</p>
      <p class="mt-1 leading-6">{text}</p>
    </section>
  );
}

function FeatureLocked() {
  const { route } = useLocation();
  return (
    <section class="rounded-xl border border-indigo-100 bg-white p-8 shadow-sm">
      <div class="flex items-start gap-4">
        <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full bg-indigo-50 text-indigo-600">
          <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="5" y="11" width="14" height="10" rx="2" />
            <path stroke-linecap="round" d="M8 11V7a4 4 0 118 0v4" />
          </svg>
        </span>
        <div class="min-w-0">
          <h2 class="text-lg font-semibold text-gray-900">{t("team.lockedTitle")}</h2>
          <p class="mt-1 text-sm leading-6 text-gray-600">{t("team.lockedDesc")}</p>
          <ul class="mt-4 space-y-1.5 text-sm text-gray-700">
            {["team.lockedPoint1", "team.lockedPoint2", "team.lockedPoint3", "team.lockedPoint4"].map((key) => (
              <li key={key} class="flex gap-2">
                <span class="text-indigo-500">•</span>
                {t(key)}
              </li>
            ))}
          </ul>
          <button type="button" class={`${primaryButton} mt-6`} onClick={() => route("/settings")}>
            {t("team.lockedAction")}
          </button>
        </div>
      </div>
    </section>
  );
}

function CodeLine({ children }: { children: string }) {
  return <code class="mt-1 block overflow-x-auto whitespace-pre rounded-lg bg-gray-900 px-3 py-2 font-mono text-xs text-gray-100">{children}</code>;
}

function EnableAccounts() {
  const local = !!authSession.value?.local_operator;
  const [username, setUsername] = useState("admin");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: Event) => {
    event.preventDefault();
    if (password.length < 8) return setError(t("team.passwordTooShort"));
    if (password !== confirm) return setError(t("team.passwordMismatch"));
    setBusy(true);
    setError("");
    try {
      await createTeamUser({
        username: username.trim(),
        password,
        display_name: displayName.trim() || undefined,
        setup_token: local ? undefined : setupToken.trim(),
      });
      // Sign the creator in at once: from a remote browser, the instance
      // requires a session from the moment the first user exists.
      await login(username.trim(), password);
      await loadAuthSession();
    } catch (err: any) {
      setError(isApiError(err) && err.code === SETUP_TOKEN_REQUIRED ? t("team.setupTokenWrong") : errorText(err, t("team.saveFailed")));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <section class="rounded-xl border border-gray-200 bg-white p-6 shadow-sm">
        <h2 class="text-lg font-semibold text-gray-900">{t("team.enableTitle")}</h2>
        <p class="mt-1 text-sm leading-6 text-gray-600">{t("team.enableDesc")}</p>
        <form onSubmit={submit} class="mt-5 space-y-3">
          {!local && (
            <label class="block text-sm font-medium text-gray-700">
              {t("team.setupToken")}
              <input class={`${inputClass} font-mono`} value={setupToken} onInput={(e) => setSetupToken((e.target as HTMLInputElement).value)} />
              <span class="mt-1 block text-xs font-normal leading-5 text-gray-500">{t("team.setupTokenHint")}</span>
            </label>
          )}
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="block text-sm font-medium text-gray-700">
              {t("auth.username")}
              <input class={inputClass} autoComplete="username" value={username} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
            </label>
            <label class="block text-sm font-medium text-gray-700">
              {t("team.displayName")}
              <input class={inputClass} value={displayName} onInput={(e) => setDisplayName((e.target as HTMLInputElement).value)} />
            </label>
          </div>
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="block text-sm font-medium text-gray-700">
              {t("auth.password")}
              <input type="password" autoComplete="new-password" class={inputClass} value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
            </label>
            <label class="block text-sm font-medium text-gray-700">
              {t("team.passwordConfirm")}
              <input type="password" autoComplete="new-password" class={inputClass} value={confirm} onInput={(e) => setConfirm((e.target as HTMLInputElement).value)} />
            </label>
          </div>
          {error && <p role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>}
          <button type="submit" disabled={busy || !username.trim() || (!local && !setupToken.trim())} class={primaryButton}>
            {busy ? t("team.saving") : t("team.enableAction")}
          </button>
        </form>
        <ul class="mt-6 space-y-2 border-t border-gray-100 pt-4 text-sm text-gray-600">
          {["team.enablePoint1", "team.enablePoint2", "team.enablePoint3"].map((key) => (
            <li key={key} class="flex gap-2">
              <span class="text-indigo-500">•</span>
              {t(key)}
            </li>
          ))}
        </ul>
      </section>
      <section class="rounded-xl border border-gray-200 bg-gray-50 p-6 text-sm text-gray-700">
        <h2 class="text-base font-semibold text-gray-900">{t("team.otherWaysTitle")}</h2>
        <p class="mt-3 font-medium text-gray-900">{t("team.otherWaysEnv")}</p>
        <p class="mt-1 text-xs leading-5 text-gray-500">{t("team.otherWaysEnvDesc")}</p>
        <CodeLine>{"CSGHUB_LITE_ADMIN_USERNAME=admin\nCSGHUB_LITE_ADMIN_PASSWORD_FILE=/run/secrets/csglite_admin"}</CodeLine>
        <p class="mt-4 font-medium text-gray-900">{t("team.otherWaysCli")}</p>
        <p class="mt-1 text-xs leading-5 text-gray-500">{t("team.otherWaysCliDesc")}</p>
        <CodeLine>{"csghub-lite user create admin --role admin"}</CodeLine>
        <p class="mt-4 font-medium text-gray-900">{t("team.otherWaysRecover")}</p>
        <p class="mt-1 text-xs leading-5 text-gray-500">{t("team.otherWaysRecoverDesc")}</p>
        <CodeLine>{"csghub-lite user reset-password admin"}</CodeLine>
      </section>
    </div>
  );
}

function TeamManager() {
  const tab = activeTab.value;
  const pending = users.value.filter((user) => user.pending).length;
  const tabs: { id: Tab; label: string; count?: number }[] = [
    { id: "members", label: t("team.tabUsers"), count: users.value.length },
    { id: "teams", label: t("team.tabTeams"), count: teams.value.length },
    { id: "invites", label: t("team.tabInvites"), count: pending },
    { id: "security", label: t("team.tabSecurity") },
  ];
  const readOnly = !isAdmin(authSession.value);
  return (
    <div class="space-y-4">
      {settings.value?.bootstrap_env_set && isAdmin(authSession.value) && (
        <Notice tone="warn" title={t("team.bootstrapEnvTitle")} text={t("team.bootstrapEnvDesc")} />
      )}
      {readOnly && <Notice tone="neutral" title={t("team.readOnlyTitle")} text={t("team.readOnlyDesc")} />}
      <div class="flex gap-1 border-b border-gray-200">
        {tabs.map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => (activeTab.value = item.id)}
            class={`-mb-px flex items-center gap-2 border-b-2 px-4 py-2.5 text-sm font-medium transition-colors ${
              tab === item.id ? "border-indigo-600 text-indigo-700" : "border-transparent text-gray-500 hover:text-gray-800"
            }`}
          >
            {item.label}
            {item.count !== undefined && (
              <span class={`rounded-full px-1.5 py-0.5 text-[11px] ${tab === item.id ? "bg-indigo-50 text-indigo-700" : "bg-gray-100 text-gray-500"}`}>{item.count}</span>
            )}
          </button>
        ))}
      </div>
      {loadError.value && <p role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{loadError.value}</p>}
      {issuedInvite.value && <InviteLinkCard />}
      {tab === "members" && <MembersPanel readOnly={readOnly} />}
      {tab === "teams" && <TeamsPanel readOnly={readOnly} />}
      {tab === "invites" && <InvitesPanel readOnly={readOnly} />}
      {tab === "security" && <SecurityPanel readOnly={readOnly} />}
    </div>
  );
}

function InviteLinkCard() {
  const issued = issuedInvite.value;
  const [copied, setCopied] = useState(false);
  if (!issued) return null;
  const link = inviteLink(issued.token);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* the link stays selectable */
    }
  };
  return (
    <section class="rounded-xl border border-indigo-200 bg-indigo-50 p-4">
      <div class="flex flex-wrap items-start justify-between gap-2">
        <div>
          <p class="text-sm font-semibold text-indigo-900">{t("team.inviteReadyTitle", userLabel(issued.user))}</p>
          <p class="mt-0.5 text-xs text-indigo-800">{t("team.inviteReadyDesc", formatDate(issued.user.invite_expires_at))}</p>
        </div>
        <button type="button" class="text-xs text-indigo-700 hover:underline" onClick={() => (issuedInvite.value = null)}>
          {t("dash.close")}
        </button>
      </div>
      <div class="mt-3 flex gap-2">
        <input readOnly value={link} class="min-w-0 flex-1 rounded-lg border border-indigo-200 bg-white px-3 py-2 font-mono text-xs text-gray-800" onFocus={(e) => (e.target as HTMLInputElement).select()} />
        <button type="button" class={primaryButton} onClick={() => void copy()}>
          {copied ? t("team.copied") : t("team.copyLink")}
        </button>
      </div>
    </section>
  );
}

type UserDialogState =
  | { mode: "invite" }
  | { mode: "create" }
  | { mode: "edit"; user: TeamUser }
  | { mode: "password"; user: TeamUser }
  | null;

function MembersPanel({ readOnly }: { readOnly: boolean }) {
  const [dialog, setDialog] = useState<UserDialogState>(null);
  const [deleting, setDeleting] = useState<TeamUser | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");
  const [query, setQuery] = useState("");
  const [roleFilter, setRoleFilter] = useState("");
  const [teamFilter, setTeamFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const me = authSession.value?.user?.id;
  const seats = maxUsers.value;
  const seatsFull = seats > 0 && users.value.length >= seats;

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return users.value.filter((user) => {
      if (needle && ![user.username, user.display_name, user.email].some((v) => v?.toLowerCase().includes(needle))) return false;
      if (roleFilter && user.role !== roleFilter) return false;
      if (teamFilter && (user.team_id || "none") !== teamFilter) return false;
      if (statusFilter && userStatus(user) !== statusFilter) return false;
      return true;
    });
  }, [users.value, query, roleFilter, teamFilter, statusFilter]);

  const toggleDisabled = async (user: TeamUser) => {
    setActionError("");
    try {
      await updateTeamUser(user.id, { disabled: !user.disabled });
      await loadTeamData();
    } catch (err: any) {
      setActionError(errorText(err, t("team.saveFailed")));
    }
  };

  const reissue = async (user: TeamUser) => {
    setActionError("");
    try {
      const updated = await reinviteTeamUser(user.id);
      issuedInvite.value = { user: updated, token: updated.invite_token || "" };
      await loadTeamData();
    } catch (err: any) {
      setActionError(errorText(err, t("team.saveFailed")));
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    setBusy(true);
    setActionError("");
    try {
      await deleteTeamUser(deleting.id);
      setDeleting(null);
      await loadTeamData();
    } catch (err: any) {
      setActionError(errorText(err, t("team.deleteFailed")));
      setDeleting(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section class="rounded-xl border border-gray-200 bg-white shadow-sm">
      <div class="flex flex-wrap items-center gap-2 border-b border-gray-100 px-5 py-4">
        <input
          type="search"
          value={query}
          onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
          placeholder={t("team.searchPlaceholder")}
          class={`${selectClass} w-56`}
        />
        <select class={selectClass} value={roleFilter} onChange={(e) => setRoleFilter((e.target as HTMLSelectElement).value)}>
          <option value="">{t("team.filterAllRoles")}</option>
          {roles.map((r) => (
            <option key={r} value={r}>
              {t(`team.role.${r}`)}
            </option>
          ))}
        </select>
        <select class={selectClass} value={teamFilter} onChange={(e) => setTeamFilter((e.target as HTMLSelectElement).value)}>
          <option value="">{t("team.filterAllTeams")}</option>
          <option value="none">{t("team.noTeam")}</option>
          {teams.value.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
            </option>
          ))}
        </select>
        <select class={selectClass} value={statusFilter} onChange={(e) => setStatusFilter((e.target as HTMLSelectElement).value)}>
          <option value="">{t("team.filterAllStatuses")}</option>
          {(["active", "pending", "mustChange", "disabled"] as const).map((status) => (
            <option key={status} value={status}>
              {t(`team.status.${status}`)}
            </option>
          ))}
        </select>
        {!readOnly && (
          <div class="ml-auto flex gap-2">
            <button type="button" class={secondaryButton} disabled={seatsFull} onClick={() => setDialog({ mode: "create" })}>
              {t("team.createDirect")}
            </button>
            <button type="button" class={primaryButton} disabled={seatsFull} onClick={() => setDialog({ mode: "invite" })}>
              {t("team.inviteMember")}
            </button>
          </div>
        )}
      </div>
      {seatsFull && !readOnly && <p class="mx-5 mt-4 rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800">{t("team.seatLimitReached", seats)}</p>}
      {actionError && <p role="alert" class="mx-5 mt-4 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{actionError}</p>}
      <div class="overflow-x-auto">
        <table class="min-w-full text-sm">
          <thead class="text-left text-xs uppercase tracking-wide text-gray-500">
            <tr>
              <th class="px-5 py-3 font-medium">{t("team.colUser")}</th>
              <th class="px-5 py-3 font-medium">{t("team.colRole")}</th>
              <th class="px-5 py-3 font-medium">{t("team.colTeam")}</th>
              <th class="px-5 py-3 font-medium">{t("team.colStatus")}</th>
              <th class="px-5 py-3 font-medium">{t("team.colLastLogin")}</th>
              {!readOnly && <th class="px-5 py-3 text-right font-medium">{t("team.colActions")}</th>}
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100">
            {loading.value && users.value.length === 0 && (
              <tr>
                <td colSpan={6} class="px-5 py-6 text-center text-gray-500">
                  {t("team.loading")}
                </td>
              </tr>
            )}
            {!loading.value && visible.length === 0 && (
              <tr>
                <td colSpan={6} class="px-5 py-8 text-center text-gray-500">
                  {t("team.noMatches")}
                </td>
              </tr>
            )}
            {visible.map((user) => (
              <tr key={user.id} class={user.disabled ? "bg-gray-50 text-gray-400" : ""}>
                <td class="px-5 py-3">
                  <div class="font-medium text-gray-900">
                    {userLabel(user)}
                    {user.id === me && <span class="ml-1.5 text-xs font-normal text-indigo-600">{t("team.you")}</span>}
                  </div>
                  <div class="text-xs text-gray-500">
                    {user.username}
                    {user.email ? ` · ${user.email}` : ""}
                  </div>
                </td>
                <td class="px-5 py-3">
                  <RoleBadge role={user.role} />
                </td>
                <td class="px-5 py-3 text-gray-700">{user.team_name || <span class="text-gray-400">{t("team.noTeam")}</span>}</td>
                <td class="px-5 py-3">
                  <StatusBadge user={user} />
                </td>
                <td class="px-5 py-3 text-gray-500">{user.last_login_at ? formatDate(user.last_login_at) : t("team.never")}</td>
                {!readOnly && (
                  <td class="whitespace-nowrap px-5 py-3 text-right">
                    {user.pending ? (
                      <>
                        <button type="button" class={linkButton} onClick={() => void reissue(user)}>
                          {t("team.newInviteLink")}
                        </button>
                        <button type="button" class="px-1.5 text-sm text-red-600 hover:underline" onClick={() => setDeleting(user)}>
                          {t("team.revokeInvite")}
                        </button>
                      </>
                    ) : (
                      <>
                        <button type="button" class={linkButton} onClick={() => setDialog({ mode: "edit", user })}>
                          {t("team.edit")}
                        </button>
                        <button type="button" class={linkButton} onClick={() => setDialog({ mode: "password", user })}>
                          {t("team.resetPassword")}
                        </button>
                        <button type="button" class="px-1.5 text-sm text-gray-600 hover:underline" onClick={() => void toggleDisabled(user)}>
                          {user.disabled ? t("team.enable") : t("team.disable")}
                        </button>
                        <button type="button" class="px-1.5 text-sm text-red-600 hover:underline" onClick={() => setDeleting(user)}>
                          {t("team.delete")}
                        </button>
                      </>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {dialog && <UserDialog state={dialog} onClose={() => setDialog(null)} />}
      <ConfirmDialog
        open={deleting !== null}
        title={deleting?.pending ? t("team.revokeInviteTitle") : t("team.deleteUserTitle")}
        name={deleting ? userLabel(deleting) : ""}
        description={deleting?.pending ? t("team.revokeInviteDesc") : t("team.deleteUserDesc")}
        confirmLabel={deleting?.pending ? t("team.revokeInvite") : t("team.delete")}
        busy={busy}
        onConfirm={() => void confirmDelete()}
        onCancel={() => setDeleting(null)}
      />
    </section>
  );
}

function userStatus(user: TeamUser): "active" | "pending" | "mustChange" | "disabled" {
  if (user.disabled) return "disabled";
  if (user.pending) return "pending";
  if (user.must_change_password) return "mustChange";
  return "active";
}

function StatusBadge({ user }: { user: TeamUser }) {
  const status = userStatus(user);
  const classes = {
    active: "bg-green-50 text-green-700",
    pending: "bg-amber-50 text-amber-700",
    mustChange: "bg-sky-50 text-sky-700",
    disabled: "bg-gray-200 text-gray-600",
  }[status];
  return (
    <span class={`rounded-full px-2 py-0.5 text-xs ${classes}`} title={status === "pending" && user.invite_expires_at ? t("team.inviteExpires", formatDate(user.invite_expires_at)) : undefined}>
      {t(`team.status.${status}`)}
    </span>
  );
}

function RoleBadge({ role }: { role: TeamRole }) {
  const classes: Record<TeamRole, string> = {
    admin: "bg-indigo-50 text-indigo-700",
    auditor: "bg-violet-50 text-violet-700",
    member: "bg-sky-50 text-sky-700",
  };
  return <span class={`rounded-full px-2 py-0.5 text-xs font-medium ${classes[role] ?? classes.member}`}>{t(`team.role.${role}`)}</span>;
}

function RolePicker({ role, onChange }: { role: TeamRole; onChange: (role: TeamRole) => void }) {
  return (
    <fieldset class="space-y-2">
      <legend class="text-sm font-medium text-gray-700">{t("team.colRole")}</legend>
      {roles.map((r) => (
        <label
          key={r}
          class={`flex cursor-pointer gap-3 rounded-lg border px-3 py-2 ${role === r ? "border-indigo-500 bg-indigo-50" : "border-gray-200 hover:bg-gray-50"}`}
        >
          <input type="radio" name="role" class="mt-1" checked={role === r} onChange={() => onChange(r)} />
          <span>
            <span class="block text-sm font-medium text-gray-900">{t(`team.role.${r}`)}</span>
            <span class="block text-xs leading-5 text-gray-500">{t(`team.roleDesc.${r}`)}</span>
          </span>
        </label>
      ))}
    </fieldset>
  );
}

function UserDialog({ state, onClose }: { state: NonNullable<UserDialogState>; onClose: () => void }) {
  const editing = state.mode === "edit" || state.mode === "password" ? state.user : null;
  const [username, setUsername] = useState(editing?.username ?? "");
  const [displayName, setDisplayName] = useState(editing?.display_name ?? "");
  const [email, setEmail] = useState(editing?.email ?? "");
  const [role, setRole] = useState<TeamRole>(editing?.role ?? "member");
  const [teamID, setTeamID] = useState(editing?.team_id ?? "");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const needsPassword = state.mode === "create" || state.mode === "password";
  const showProfile = state.mode !== "password";

  const submit = async (event: Event) => {
    event.preventDefault();
    if (needsPassword) {
      if (password.length < 8) return setError(t("team.passwordTooShort"));
      if (password !== confirm) return setError(t("team.passwordMismatch"));
    }
    setBusy(true);
    setError("");
    try {
      if (state.mode === "invite" || state.mode === "create") {
        const created = await createTeamUser({
          username: username.trim(),
          password: state.mode === "create" ? password : undefined,
          invite: state.mode === "invite",
          display_name: displayName.trim() || undefined,
          email: email.trim() || undefined,
          role,
          team_id: teamID || undefined,
        });
        if (created.invite_token) issuedInvite.value = { user: created, token: created.invite_token };
      } else if (state.mode === "edit") {
        await updateTeamUser(state.user.id, { display_name: displayName.trim(), email: email.trim(), role, team_id: teamID });
      } else {
        await updateTeamUser(state.user.id, { password });
      }
      await loadTeamData();
      onClose();
    } catch (err: any) {
      setError(errorText(err, t("team.saveFailed")));
    } finally {
      setBusy(false);
    }
  };

  const titles = {
    invite: t("team.inviteMember"),
    create: t("team.createDirect"),
    edit: t("team.editUser"),
    password: editing ? t("team.resetPasswordFor", userLabel(editing)) : "",
  };
  const hints = {
    invite: t("team.inviteHint"),
    create: t("team.createDirectHint"),
    edit: "",
    password: t("team.resetPasswordDesc"),
  };
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4" onClick={() => !busy && onClose()}>
      <form
        role="dialog"
        aria-modal="true"
        onSubmit={submit}
        onClick={(event) => event.stopPropagation()}
        class="max-h-[90vh] w-full max-w-lg space-y-3 overflow-y-auto rounded-2xl border border-gray-200 bg-white p-6 shadow-2xl"
      >
        <h2 class="text-lg font-semibold text-gray-900">{titles[state.mode]}</h2>
        {hints[state.mode] && <p class="text-sm leading-6 text-gray-600">{hints[state.mode]}</p>}
        {showProfile && (
          <>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block text-sm font-medium text-gray-700">
                {t("auth.username")}
                <input class={inputClass} value={username} disabled={state.mode === "edit"} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
              </label>
              <label class="block text-sm font-medium text-gray-700">
                {t("team.displayName")}
                <input class={inputClass} value={displayName} onInput={(e) => setDisplayName((e.target as HTMLInputElement).value)} />
              </label>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block text-sm font-medium text-gray-700">
                {t("team.email")}
                <input type="email" class={inputClass} value={email} onInput={(e) => setEmail((e.target as HTMLInputElement).value)} />
              </label>
              <label class="block text-sm font-medium text-gray-700">
                {t("team.colTeam")}
                <select class={inputClass} value={teamID} onChange={(e) => setTeamID((e.target as HTMLSelectElement).value)}>
                  <option value="">{t("team.noTeam")}</option>
                  {teams.value.map((group) => (
                    <option key={group.id} value={group.id}>
                      {group.name}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <RolePicker role={role} onChange={setRole} />
          </>
        )}
        {needsPassword && (
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="block text-sm font-medium text-gray-700">
              {state.mode === "password" ? t("auth.newPassword") : t("team.temporaryPassword")}
              <input type="password" autoComplete="new-password" class={inputClass} value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
            </label>
            <label class="block text-sm font-medium text-gray-700">
              {t("team.passwordConfirm")}
              <input type="password" autoComplete="new-password" class={inputClass} value={confirm} onInput={(e) => setConfirm((e.target as HTMLInputElement).value)} />
            </label>
          </div>
        )}
        {error && <p role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>}
        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class={secondaryButton} disabled={busy} onClick={onClose}>
            {t("team.cancel")}
          </button>
          <button type="submit" class={primaryButton} disabled={busy || (showProfile && !username.trim())}>
            {busy ? t("team.saving") : state.mode === "invite" ? t("team.sendInvite") : t("team.save")}
          </button>
        </div>
      </form>
    </div>
  );
}

function InvitesPanel({ readOnly }: { readOnly: boolean }) {
  const pending = users.value.filter((user) => user.pending);
  const [revoking, setRevoking] = useState<TeamUser | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");

  const reissue = async (user: TeamUser) => {
    setActionError("");
    try {
      const updated = await reinviteTeamUser(user.id);
      issuedInvite.value = { user: updated, token: updated.invite_token || "" };
      await loadTeamData();
    } catch (err: any) {
      setActionError(errorText(err, t("team.saveFailed")));
    }
  };
  const revoke = async () => {
    if (!revoking) return;
    setBusy(true);
    try {
      await deleteTeamUser(revoking.id);
      setRevoking(null);
      await loadTeamData();
    } catch (err: any) {
      setActionError(errorText(err, t("team.deleteFailed")));
      setRevoking(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section class="rounded-xl border border-gray-200 bg-white shadow-sm">
      <div class="border-b border-gray-100 px-5 py-4">
        <h2 class="text-base font-semibold text-gray-900">{t("team.invitesTitle")}</h2>
        <p class="text-sm text-gray-500">{t("team.invitesDesc")}</p>
      </div>
      {actionError && <p role="alert" class="mx-5 mt-4 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{actionError}</p>}
      {pending.length === 0 ? (
        <p class="px-5 py-8 text-center text-sm text-gray-500">{t("team.invitesEmpty")}</p>
      ) : (
        <ul class="divide-y divide-gray-100">
          {pending.map((user) => {
            const expired = !user.invite_expires_at;
            return (
              <li key={user.id} class="flex flex-wrap items-center justify-between gap-3 px-5 py-3">
                <div class="min-w-0">
                  <p class="font-medium text-gray-900">
                    {userLabel(user)} <RoleBadge role={user.role} />
                  </p>
                  <p class={`text-xs ${expired ? "text-red-600" : "text-gray-500"}`}>
                    {expired ? t("team.inviteExpired") : t("team.inviteExpires", formatDate(user.invite_expires_at))}
                  </p>
                </div>
                {!readOnly && (
                  <div class="text-sm">
                    <button type="button" class={linkButton} onClick={() => void reissue(user)}>
                      {t("team.newInviteLink")}
                    </button>
                    <button type="button" class="px-1.5 text-red-600 hover:underline" onClick={() => setRevoking(user)}>
                      {t("team.revokeInvite")}
                    </button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <ConfirmDialog
        open={revoking !== null}
        title={t("team.revokeInviteTitle")}
        name={revoking ? userLabel(revoking) : ""}
        description={t("team.revokeInviteDesc")}
        confirmLabel={t("team.revokeInvite")}
        busy={busy}
        onConfirm={() => void revoke()}
        onCancel={() => setRevoking(null)}
      />
    </section>
  );
}

function TeamsPanel({ readOnly }: { readOnly: boolean }) {
  const [dialog, setDialog] = useState<{ team: TeamGroup | null } | null>(null);
  const [deleting, setDeleting] = useState<TeamGroup | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");

  const confirmDelete = async () => {
    if (!deleting) return;
    setBusy(true);
    setActionError("");
    try {
      await deleteTeam(deleting.id);
      setDeleting(null);
      await loadTeamData();
    } catch (err: any) {
      setActionError(errorText(err, t("team.deleteFailed")));
      setDeleting(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section class="rounded-xl border border-gray-200 bg-white shadow-sm">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4">
        <div>
          <h2 class="text-base font-semibold text-gray-900">{t("team.teamsTitle")}</h2>
          <p class="text-sm text-gray-500">{t("team.teamsDesc")}</p>
        </div>
        {!readOnly && (
          <button type="button" class={primaryButton} onClick={() => setDialog({ team: null })}>
            {t("team.addTeam")}
          </button>
        )}
      </div>
      {actionError && <p role="alert" class="mx-5 mt-4 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{actionError}</p>}
      {teams.value.length === 0 ? (
        <p class="px-5 py-8 text-center text-sm text-gray-500">{t("team.teamsEmpty")}</p>
      ) : (
        <ul class="divide-y divide-gray-100">
          {teams.value.map((group) => (
            <li key={group.id} class="flex flex-wrap items-center justify-between gap-3 px-5 py-3">
              <div class="min-w-0">
                <p class="font-medium text-gray-900">{group.name}</p>
                <p class="text-xs text-gray-500">
                  {t("team.memberCount", group.member_count)}
                  {group.description ? ` · ${group.description}` : ""}
                </p>
              </div>
              {!readOnly && (
                <div class="text-sm">
                  <button type="button" class={linkButton} onClick={() => setDialog({ team: group })}>
                    {t("team.edit")}
                  </button>
                  <button type="button" class="px-1.5 text-red-600 hover:underline" onClick={() => setDeleting(group)}>
                    {t("team.delete")}
                  </button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {dialog && <TeamDialog team={dialog.team} onClose={() => setDialog(null)} />}
      <ConfirmDialog
        open={deleting !== null}
        title={t("team.deleteTeamTitle")}
        name={deleting?.name ?? ""}
        description={t("team.deleteTeamDesc")}
        confirmLabel={t("team.delete")}
        busy={busy}
        onConfirm={() => void confirmDelete()}
        onCancel={() => setDeleting(null)}
      />
    </section>
  );
}

function TeamDialog({ team, onClose }: { team: TeamGroup | null; onClose: () => void }) {
  const [name, setName] = useState(team?.name ?? "");
  const [description, setDescription] = useState(team?.description ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: Event) => {
    event.preventDefault();
    if (!name.trim()) return setError(t("team.teamNameRequired"));
    setBusy(true);
    setError("");
    try {
      if (team) await updateTeam(team.id, name.trim(), description.trim());
      else await createTeam(name.trim(), description.trim());
      await loadTeamData();
      onClose();
    } catch (err: any) {
      setError(errorText(err, t("team.saveFailed")));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4" onClick={() => !busy && onClose()}>
      <form
        role="dialog"
        aria-modal="true"
        onSubmit={submit}
        onClick={(event) => event.stopPropagation()}
        class="w-full max-w-md space-y-3 rounded-2xl border border-gray-200 bg-white p-6 shadow-2xl"
      >
        <h2 class="text-lg font-semibold text-gray-900">{team ? t("team.editTeam") : t("team.addTeam")}</h2>
        <label class="block text-sm font-medium text-gray-700">
          {t("team.teamName")}
          <input class={inputClass} value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        </label>
        <label class="block text-sm font-medium text-gray-700">
          {t("team.teamDescription")}
          <input class={inputClass} value={description} onInput={(e) => setDescription((e.target as HTMLInputElement).value)} />
        </label>
        {error && <p role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>}
        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class={secondaryButton} disabled={busy} onClick={onClose}>
            {t("team.cancel")}
          </button>
          <button type="submit" class={primaryButton} disabled={busy}>
            {busy ? t("team.saving") : t("team.save")}
          </button>
        </div>
      </form>
    </div>
  );
}

const sessionChoices = [8, 24, 24 * 7, 24 * 30];

function SecurityPanel({ readOnly }: { readOnly: boolean }) {
  const current = settings.value;
  const [sessionHours, setSessionHours] = useState(current?.session_hours ?? 168);
  const [maxFailed, setMaxFailed] = useState(current?.max_failed_logins ?? 5);
  const [lockout, setLockout] = useState(current?.lockout_minutes ?? 5);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!current) return;
    setSessionHours(current.session_hours);
    setMaxFailed(current.max_failed_logins);
    setLockout(current.lockout_minutes);
  }, [current]);

  const save = async (event: Event) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    setMessage("");
    try {
      settings.value = await updateTeamSettings({ session_hours: sessionHours, max_failed_logins: maxFailed, lockout_minutes: lockout });
      setMessage(t("team.securitySaved"));
    } catch (err: any) {
      setError(errorText(err, t("team.saveFailed")));
    } finally {
      setBusy(false);
    }
  };

  const sessionLabel = (hours: number) => (hours % 24 === 0 ? t("team.days", hours / 24) : t("team.hours", hours));
  return (
    <div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <form onSubmit={save} class="space-y-4 rounded-xl border border-gray-200 bg-white p-6 shadow-sm">
        <div>
          <h2 class="text-base font-semibold text-gray-900">{t("team.securityTitle")}</h2>
          <p class="text-sm text-gray-500">{t("team.securityDesc")}</p>
        </div>
        <label class="block text-sm font-medium text-gray-700">
          {t("team.sessionLength")}
          <select class={inputClass} value={sessionHours} disabled={readOnly} onChange={(e) => setSessionHours(Number((e.target as HTMLSelectElement).value))}>
            {[...new Set([...sessionChoices, sessionHours])].sort((a, b) => a - b).map((hours) => (
              <option key={hours} value={hours}>
                {sessionLabel(hours)}
              </option>
            ))}
          </select>
          <span class="mt-1 block text-xs font-normal text-gray-500">{t("team.sessionLengthHint")}</span>
        </label>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block text-sm font-medium text-gray-700">
            {t("team.maxFailedLogins")}
            <input type="number" min={3} max={50} class={inputClass} value={maxFailed} disabled={readOnly} onInput={(e) => setMaxFailed(Number((e.target as HTMLInputElement).value))} />
          </label>
          <label class="block text-sm font-medium text-gray-700">
            {t("team.lockoutMinutes")}
            <input type="number" min={1} max={1440} class={inputClass} value={lockout} disabled={readOnly} onInput={(e) => setLockout(Number((e.target as HTMLInputElement).value))} />
          </label>
        </div>
        <p class="text-xs text-gray-500">{t("team.lockoutHint", maxFailed, lockout)}</p>
        {error && <p role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>}
        {message && <p class="rounded-lg bg-green-50 px-3 py-2 text-sm text-green-700">{message}</p>}
        {!readOnly && (
          <button type="submit" class={primaryButton} disabled={busy}>
            {busy ? t("team.saving") : t("team.save")}
          </button>
        )}
      </form>
      <section class="space-y-4 rounded-xl border border-gray-200 bg-gray-50 p-6 text-sm text-gray-700">
        <div>
          <h2 class="text-base font-semibold text-gray-900">{t("team.accessRulesTitle")}</h2>
          <ul class="mt-2 space-y-1.5 text-sm leading-6">
            {["team.accessRule1", "team.accessRule2", "team.accessRule3", "team.accessRule4"].map((key) => (
              <li key={key} class="flex gap-2">
                <span class="text-indigo-500">•</span>
                {t(key)}
              </li>
            ))}
          </ul>
        </div>
        <div>
          <p class="font-medium text-gray-900">{t("team.otherWaysRecover")}</p>
          <p class="mt-1 text-xs leading-5 text-gray-500">{t("team.otherWaysRecoverDesc")}</p>
          <CodeLine>{"csghub-lite user reset-password <username>"}</CodeLine>
        </div>
        <div>
          <p class="font-medium text-gray-900">{t("team.bootstrapEnvLabel")}</p>
          <p class={`mt-1 text-xs leading-5 ${settings.value?.bootstrap_env_set ? "text-amber-700" : "text-gray-500"}`}>
            {settings.value?.bootstrap_env_set ? t("team.bootstrapEnvDesc") : t("team.bootstrapEnvClear")}
          </p>
        </div>
      </section>
    </div>
  );
}
