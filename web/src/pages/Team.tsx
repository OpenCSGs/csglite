import { signal } from "@preact/signals";
import { useEffect, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import {
  createTeam,
  createTeamUser,
  deleteTeam,
  deleteTeamUser,
  getTeams,
  getTeamUsers,
  isFeatureNotLicensedError,
  login,
  TEAM_MANAGEMENT_FEATURE,
  updateTeam,
  updateTeamUser,
} from "../api/client";
import type { TeamGroup, TeamRole, TeamUser } from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { authSession, isAdmin, loadAuthSession, userLabel } from "../auth";
import { locale, t } from "../i18n";
import { formatLicenseDate, licenseState } from "../license";

const roles: TeamRole[] = ["admin", "member", "viewer"];

const users = signal<TeamUser[]>([]);
const teams = signal<TeamGroup[]>([]);
const maxUsers = signal(0);
const loading = signal(false);
const loadError = signal("");
const activeTab = signal<"users" | "teams">("users");

async function loadTeamData() {
  loading.value = true;
  loadError.value = "";
  try {
    const [userList, teamList] = await Promise.all([getTeamUsers(), getTeams()]);
    users.value = userList.users;
    maxUsers.value = userList.max_users;
    teams.value = teamList;
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

const inputClass =
  "mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500";
const primaryButton =
  "rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-60";
const secondaryButton =
  "rounded-lg border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60";

export function Team() {
  void locale.value;
  const session = authSession.value;
  const license = licenseState.value;
  const licensed = (license?.features ?? []).includes(TEAM_MANAGEMENT_FEATURE);
  const accountsOn = !!session?.accounts_enabled;

  useEffect(() => {
    if (licensed && accountsOn && isAdmin(session)) void loadTeamData();
  }, [licensed, accountsOn]);

  let body;
  if (!isAdmin(session)) {
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
      <div>
        <p class="text-xs font-semibold uppercase tracking-[0.28em] text-indigo-500">{t("team.eyebrow")}</p>
        <h1 class="mt-2 text-3xl font-bold tracking-tight text-gray-950">{t("team.title")}</h1>
        <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-600">{t("team.subtitle")}</p>
      </div>
      {body}
    </div>
  );
}

function Notice({ tone, title, text }: { tone: "neutral" | "warn"; title: string; text: string }) {
  const classes = tone === "warn" ? "border-amber-200 bg-amber-50 text-amber-900" : "border-gray-200 bg-white text-gray-700";
  return (
    <section class={`rounded-xl border p-6 text-sm ${classes}`}>
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

function EnableAccounts() {
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: Event) => {
    event.preventDefault();
    if (password.length < 8) return setError(t("team.passwordTooShort"));
    if (password !== confirm) return setError(t("team.passwordMismatch"));
    setBusy(true);
    setError("");
    try {
      await createTeamUser({ username: username.trim(), password, display_name: displayName.trim() || undefined });
      // Sign the creator in at once: from a remote browser, the instance
      // requires a session from the moment the first user exists.
      await login(username.trim(), password);
      await loadAuthSession();
    } catch (err: any) {
      setError(errorText(err, t("team.saveFailed")));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section class="grid gap-6 rounded-xl border border-gray-200 bg-white p-6 shadow-sm lg:grid-cols-[1fr_1fr]">
      <div>
        <h2 class="text-lg font-semibold text-gray-900">{t("team.enableTitle")}</h2>
        <p class="mt-1 text-sm leading-6 text-gray-600">{t("team.enableDesc")}</p>
        <ul class="mt-4 space-y-2 text-sm text-gray-700">
          {["team.enablePoint1", "team.enablePoint2", "team.enablePoint3"].map((key) => (
            <li key={key} class="flex gap-2">
              <span class="text-indigo-500">•</span>
              {t(key)}
            </li>
          ))}
        </ul>
      </div>
      <form onSubmit={submit} class="space-y-3">
        <label class="block text-sm font-medium text-gray-700">
          {t("auth.username")}
          <input class={inputClass} autoComplete="username" value={username} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
        </label>
        <label class="block text-sm font-medium text-gray-700">
          {t("team.displayName")}
          <input class={inputClass} value={displayName} onInput={(e) => setDisplayName((e.target as HTMLInputElement).value)} />
        </label>
        <label class="block text-sm font-medium text-gray-700">
          {t("auth.password")}
          <input type="password" autoComplete="new-password" class={inputClass} value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
        </label>
        <label class="block text-sm font-medium text-gray-700">
          {t("team.passwordConfirm")}
          <input type="password" autoComplete="new-password" class={inputClass} value={confirm} onInput={(e) => setConfirm((e.target as HTMLInputElement).value)} />
        </label>
        {error && <p role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>}
        <button type="submit" disabled={busy || !username.trim()} class={primaryButton}>
          {busy ? t("team.saving") : t("team.enableAction")}
        </button>
      </form>
    </section>
  );
}

function TeamManager() {
  const tab = activeTab.value;
  const tabClass = (active: boolean) =>
    `rounded-lg px-4 py-2 text-sm font-medium transition-colors ${active ? "bg-indigo-50 text-indigo-700" : "text-gray-600 hover:bg-gray-50"}`;
  return (
    <div class="space-y-4">
      <div class="flex gap-2">
        <button type="button" class={tabClass(tab === "users")} onClick={() => (activeTab.value = "users")}>
          {t("team.tabUsers")}
        </button>
        <button type="button" class={tabClass(tab === "teams")} onClick={() => (activeTab.value = "teams")}>
          {t("team.tabTeams")}
        </button>
      </div>
      {loadError.value && <p role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">{loadError.value}</p>}
      {tab === "users" ? <UsersPanel /> : <TeamsPanel />}
    </div>
  );
}

type UserDialogState = { mode: "create" } | { mode: "edit"; user: TeamUser } | { mode: "password"; user: TeamUser } | null;

function UsersPanel() {
  const [dialog, setDialog] = useState<UserDialogState>(null);
  const [deleting, setDeleting] = useState<TeamUser | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");
  const me = authSession.value?.user?.id;
  const seats = maxUsers.value;
  const count = users.value.length;

  const toggleDisabled = async (user: TeamUser) => {
    setActionError("");
    try {
      await updateTeamUser(user.id, { disabled: !user.disabled });
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
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4">
        <div>
          <h2 class="text-base font-semibold text-gray-900">{t("team.usersTitle")}</h2>
          <p class="text-sm text-gray-500">{seats > 0 ? t("team.seatsUsed", count, seats) : t("team.seatsUnlimited", count)}</p>
        </div>
        <button type="button" class={primaryButton} disabled={seats > 0 && count >= seats} onClick={() => setDialog({ mode: "create" })}>
          {t("team.addUser")}
        </button>
      </div>
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
              <th class="px-5 py-3 text-right font-medium">{t("team.colActions")}</th>
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
            {users.value.map((user) => (
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
                  {user.disabled ? (
                    <span class="rounded-full bg-gray-200 px-2 py-0.5 text-xs text-gray-600">{t("team.statusDisabled")}</span>
                  ) : (
                    <span class="rounded-full bg-green-50 px-2 py-0.5 text-xs text-green-700">{t("team.statusActive")}</span>
                  )}
                </td>
                <td class="px-5 py-3 text-gray-500">{user.last_login_at ? formatLicenseDate(user.last_login_at, locale.value) : t("team.never")}</td>
                <td class="whitespace-nowrap px-5 py-3 text-right">
                  <button type="button" class="px-2 text-indigo-600 hover:underline" onClick={() => setDialog({ mode: "edit", user })}>
                    {t("team.edit")}
                  </button>
                  <button type="button" class="px-2 text-indigo-600 hover:underline" onClick={() => setDialog({ mode: "password", user })}>
                    {t("team.resetPassword")}
                  </button>
                  <button type="button" class="px-2 text-gray-600 hover:underline" onClick={() => void toggleDisabled(user)}>
                    {user.disabled ? t("team.enable") : t("team.disable")}
                  </button>
                  <button type="button" class="px-2 text-red-600 hover:underline" onClick={() => setDeleting(user)}>
                    {t("team.delete")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {dialog && <UserDialog state={dialog} onClose={() => setDialog(null)} />}
      <ConfirmDialog
        open={deleting !== null}
        title={t("team.deleteUserTitle")}
        name={deleting ? userLabel(deleting) : ""}
        description={t("team.deleteUserDesc")}
        confirmLabel={t("team.delete")}
        busy={busy}
        onConfirm={() => void confirmDelete()}
        onCancel={() => setDeleting(null)}
      />
    </section>
  );
}

function RoleBadge({ role }: { role: TeamRole }) {
  const classes: Record<TeamRole, string> = {
    admin: "bg-indigo-50 text-indigo-700",
    member: "bg-sky-50 text-sky-700",
    viewer: "bg-gray-100 text-gray-600",
  };
  return <span class={`rounded-full px-2 py-0.5 text-xs font-medium ${classes[role] ?? classes.viewer}`}>{t(`team.role.${role}`)}</span>;
}

function UserDialog({ state, onClose }: { state: NonNullable<UserDialogState>; onClose: () => void }) {
  const editing = state.mode === "create" ? null : state.user;
  const [username, setUsername] = useState(editing?.username ?? "");
  const [displayName, setDisplayName] = useState(editing?.display_name ?? "");
  const [email, setEmail] = useState(editing?.email ?? "");
  const [role, setRole] = useState<TeamRole>(editing?.role ?? "member");
  const [teamID, setTeamID] = useState(editing?.team_id ?? "");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const needsPassword = state.mode !== "edit";

  const submit = async (event: Event) => {
    event.preventDefault();
    if (needsPassword) {
      if (password.length < 8) return setError(t("team.passwordTooShort"));
      if (password !== confirm) return setError(t("team.passwordMismatch"));
    }
    setBusy(true);
    setError("");
    try {
      if (state.mode === "create") {
        await createTeamUser({
          username: username.trim(),
          password,
          display_name: displayName.trim() || undefined,
          email: email.trim() || undefined,
          role,
          team_id: teamID || undefined,
        });
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

  const title = state.mode === "create" ? t("team.addUser") : state.mode === "edit" ? t("team.editUser") : t("team.resetPasswordFor", userLabel(state.user));
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4" onClick={() => !busy && onClose()}>
      <form
        role="dialog"
        aria-modal="true"
        onSubmit={submit}
        onClick={(event) => event.stopPropagation()}
        class="w-full max-w-md space-y-3 rounded-2xl border border-gray-200 bg-white p-6 shadow-2xl"
      >
        <h2 class="text-lg font-semibold text-gray-900">{title}</h2>
        {state.mode === "password" && <p class="text-sm text-gray-600">{t("team.resetPasswordDesc")}</p>}
        {state.mode !== "password" && (
          <>
            <label class="block text-sm font-medium text-gray-700">
              {t("auth.username")}
              <input class={inputClass} value={username} disabled={state.mode === "edit"} onInput={(e) => setUsername((e.target as HTMLInputElement).value)} />
            </label>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block text-sm font-medium text-gray-700">
                {t("team.displayName")}
                <input class={inputClass} value={displayName} onInput={(e) => setDisplayName((e.target as HTMLInputElement).value)} />
              </label>
              <label class="block text-sm font-medium text-gray-700">
                {t("team.email")}
                <input type="email" class={inputClass} value={email} onInput={(e) => setEmail((e.target as HTMLInputElement).value)} />
              </label>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block text-sm font-medium text-gray-700">
                {t("team.colRole")}
                <select class={inputClass} value={role} onChange={(e) => setRole((e.target as HTMLSelectElement).value as TeamRole)}>
                  {roles.map((r) => (
                    <option key={r} value={r}>
                      {t(`team.role.${r}`)}
                    </option>
                  ))}
                </select>
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
            <p class="text-xs leading-5 text-gray-500">{t(`team.roleDesc.${role}`)}</p>
          </>
        )}
        {needsPassword && (
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="block text-sm font-medium text-gray-700">
              {state.mode === "password" ? t("auth.newPassword") : t("auth.password")}
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
          <button type="submit" class={primaryButton} disabled={busy || (state.mode === "create" && !username.trim())}>
            {busy ? t("team.saving") : t("team.save")}
          </button>
        </div>
      </form>
    </div>
  );
}

function TeamsPanel() {
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
        <button type="button" class={primaryButton} onClick={() => setDialog({ team: null })}>
          {t("team.addTeam")}
        </button>
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
              <div class="text-sm">
                <button type="button" class="px-2 text-indigo-600 hover:underline" onClick={() => setDialog({ team: group })}>
                  {t("team.edit")}
                </button>
                <button type="button" class="px-2 text-red-600 hover:underline" onClick={() => setDeleting(group)}>
                  {t("team.delete")}
                </button>
              </div>
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
