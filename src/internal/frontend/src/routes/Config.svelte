<script lang="ts">
  // Config — spec §9.4 (Phase 6). Pure re-skin: NO behavior change (decision D5). The Save button
  // is still the only write path — no autosave, no debounce — because `PUT /config` validates
  // everything at once and does filesystem I/O (`Librarian.ProbePath`), so saving mid-typing would
  // produce a 400 on every keystroke.
  //
  // The new layout is the 196px side index with ONE group visible at a time. The list fields
  // (anilist_usernames, mal_usernames, excluded_lists) swapped the "input + button +" pair for
  // artboard 1e's ChipsInput.
  import { onDestroy, onMount } from "svelte";
  import { ArrowUpRight, Check } from "@lucide/svelte";
  import {
    getConfig,
    updateConfig,
    triggerCheck,
    testTorrentClient,
    shokoLogin,
    getShokoStatus,
    shokoSync,
    SECRET_PLACEHOLDER,
    type Config,
    type DownloadRoot,
    type ShokoDownloadFolder,
    type ShokoImportFolder,
    type ShokoStatus,
    type TorrentClientTestResult,
  } from "../lib/api/client.js";
  import Loading from "../components/Loading.svelte";
  import Input from "../components/Input.svelte";
  import Button from "../components/ui/Button.svelte";
  import ChipsInput from "../components/ui/ChipsInput.svelte";
  import Toggle from "../components/ui/Toggle.svelte";
  import { toast } from "../lib/stores/toast.js";
  import * as m from "../lib/i18n/messages.js";
  import { locale } from "../lib/stores/locale.js";
  import { setUnsaved, snapshot } from "../lib/stores/unsavedChanges.js";
  import {
    ONBOARDING_STEP_IDS,
    onboardingDismissed,
    onboardingDone,
  } from "../lib/stores/onboarding.js";

  type GroupId = "library" | "anilist" | "downloads" | "search" | "client" | "shoko";

  $: T = $locale && {
    title: m.config_title(),
    subtitle: m.config_subtitle(),
    missingBanner: m.config_missing_banner(),
    loading: m.config_loading(),
    navLabel: m.config_nav_label(),
    requiredLegend: m.config_required_legend(),
    groupMissing: m.config_group_missing(),
    chipsPlaceholder: m.config_chips_placeholder(),
    linkPriorities: m.nav_priorities(),
    linkNotifications: m.nav_notifications(),
    labelUsername: m.config_label_username(),
    hintAnilistUsernames: m.config_hint_anilist_usernames(),
    labelMalUsernames: m.config_label_mal_usernames(),
    hintMalUsernames: m.config_hint_mal_usernames(),
    labelCompletedPath: m.config_label_completed_path(),
    hintCompletedPath: m.config_hint_completed_path(),
    hintCompletedPathDirect: m.config_hint_completed_path_direct(),
    labelDeleteWatched: m.config_label_delete_watched(),
    labelWatchedKeep: m.config_label_watched_keep(),
    hintWatchedKeep: m.config_hint_watched_keep(),
    labelCheckInterval: m.config_label_check_interval(),
    labelMaxEpisodes: m.config_label_max_episodes(),
    hintMaxEpisodes: m.config_hint_max_episodes(),
    labelRetryLimit: m.config_label_retry_limit(),
    labelMaxBatchSize: m.config_label_max_batch_size(),
    hintMaxBatchSize: m.config_hint_max_batch_size(),
    labelMaxEpisodeSize: m.config_label_max_episode_size(),
    hintMaxEpisodeSize: m.config_hint_max_episode_size(),
    labelMinSeeders: m.config_label_min_seeders(),
    hintMinSeeders: m.config_hint_min_seeders(),
    labelMaxSearchPages: m.config_label_max_search_pages(),
    hintMaxSearchPages: m.config_hint_max_search_pages(),
    labelNyaaRequestDelay: m.config_label_nyaa_request_delay(),
    hintNyaaRequestDelay: m.config_hint_nyaa_request_delay(),
    labelMinFreeDisk: m.config_label_min_free_disk(),
    hintMinFreeDisk: m.config_hint_min_free_disk(),
    labelMaxConcurrent: m.config_label_max_concurrent(),
    hintMaxConcurrent: m.config_hint_max_concurrent(),
    labelDisableSeeding: m.config_label_disable_seeding(),
    hintDisableSeeding: m.config_hint_disable_seeding(),
    labelCheckForUpdates: m.config_label_check_for_updates(),
    hintCheckForUpdates: m.config_hint_check_for_updates(),
    labelRenameJellyfin: m.config_label_rename_jellyfin(),
    hintRenameJellyfin: m.config_hint_rename_jellyfin(),
    labelDirectMode: m.config_label_direct_mode(),
    hintDirectMode: m.config_hint_direct_mode(),
    labelAutoDeleteDownloads: m.config_label_auto_delete_downloads(),
    hintAutoDeleteDownloads: m.config_hint_auto_delete_downloads(),
    labelPartFiles: m.config_label_part_files(),
    hintPartFiles: m.config_hint_part_files(),
    labelIncompletePath: m.config_label_incomplete_path(),
    labelUseQBittorrent: m.config_label_use_qbittorrent(),
    hintUseQBittorrent: m.config_hint_use_qbittorrent(),
    labelQBittorrentURL: m.config_label_qbittorrent_url(),
    hintQBittorrentURL: m.config_hint_qbittorrent_url(),
    labelQBittorrentUsername: m.config_label_qbittorrent_username(),
    labelQBittorrentPassword: m.config_label_qbittorrent_password(),
    hintQBittorrentPassword: m.config_hint_qbittorrent_password(),
    labelQBittorrentAPIKey: m.config_label_qbittorrent_api_key(),
    hintQBittorrentAPIKey: m.config_hint_qbittorrent_api_key(),
    labelQBittorrentCategory: m.config_label_qbittorrent_category(),
    hintQBittorrentCategory: m.config_hint_qbittorrent_category(),
    labelQBittorrentManage: m.config_label_qbittorrent_manage_settings(),
    hintQBittorrentManage: m.config_hint_qbittorrent_manage_settings(),
    hintMaxConcurrentQBitManaged: m.config_hint_max_concurrent_qbittorrent_managed(),
    hintMaxConcurrentQBitOwn: m.config_hint_max_concurrent_qbittorrent_own(),
    hintDisableSeedingQBit: m.config_hint_disable_seeding_qbittorrent(),
    labelQBitUploadFloor: m.config_label_qbittorrent_disable_seeding_upload(),
    hintQBitUploadFloor: m.config_hint_qbittorrent_disable_seeding_upload(),
    labelQBittorrentPathMap: m.config_label_qbittorrent_path_map(),
    hintQBittorrentPathMap: m.config_hint_qbittorrent_path_map(),
    pathMapApp: m.config_path_map_app(),
    pathMapQBittorrent: m.config_path_map_qbittorrent(),
    pathMapAdd: m.config_path_map_add(),
    pathMapRemove: m.config_path_map_remove(),
    btnTestQBittorrent: m.config_btn_test_qbittorrent(),
    qbittorrentTesting: m.config_qbittorrent_testing(),
    qbittorrentNeedsDirect: m.config_qbittorrent_needs_direct(),
    hintIncompletePath: m.config_hint_incomplete_path(),
    labelIncompleteMinFree: m.config_label_incomplete_min_free_disk(),
    hintIncompleteMinFree: m.config_hint_incomplete_min_free_disk(),
    labelSeasonNumbering: m.config_label_season_numbering(),
    hintSeasonNumbering: m.config_hint_season_numbering(),
    labelExcludedList: m.config_label_excluded_list(),
    hintExcludedList: m.config_hint_excluded_list(),
    labelDownloadStatuses: m.config_label_download_statuses(),
    hintDownloadStatuses: m.config_hint_download_statuses(),
    labelDownloadMediaStatuses: m.config_label_download_media_statuses(),
    hintDownloadMediaStatuses: m.config_hint_download_media_statuses(),
    labelDeleteStatuses: m.config_label_delete_statuses(),
    hintDeleteStatuses: m.config_hint_delete_statuses(),
    statusLabels: {
      CURRENT:   m.config_status_current(),
      REPEATING: m.config_status_repeating(),
      PLANNING:  m.config_status_planning(),
      PAUSED:    m.config_status_paused(),
      DROPPED:   m.config_status_dropped(),
      COMPLETED: m.config_status_completed(),
      RELEASING: m.config_status_releasing(),
      FINISHED:  m.config_status_finished(),
      CANCELLED: m.config_status_cancelled(),
      HIATUS:    m.config_status_hiatus(),
    } as Record<string, string>,
    onboardingRestoreLabel: m.onboarding_restore_label(),
    onboardingRestoreHint: m.onboarding_restore_hint(),
    onboardingRestoreButton: m.onboarding_restore_button(),
    btnRunCheck: m.config_btn_run_check(),
    btnSave: m.config_btn_save(),
    btnSaving: m.config_btn_saving(),
  }

  /**
   * Index order. Library first because it hosts the screen's only required field — and it's where
   * `#/config?missingConfig=true` sends the user.
   *
   * `advanced` draws the divider above the item: "Torrent search" is fine-tuning of torrent
   * selection, not something a new install needs to touch.
   */
  $: groups = ($locale && [
    { id: "library" as GroupId, label: m.config_section_library(), advanced: false },
    { id: "anilist" as GroupId, label: m.config_section_anilist(), advanced: false },
    { id: "downloads" as GroupId, label: m.config_section_downloads(), advanced: false },
    { id: "client" as GroupId, label: m.config_section_client(), advanced: false },
    { id: "shoko" as GroupId, label: m.config_section_shoko(), advanced: false },
    { id: "search" as GroupId, label: m.config_section_search(), advanced: true },
  ]) || [];

  let activeGroup: GroupId = "library";

  // Download folders (decisions/154): the first add also lists the library, which stops being a
  // folder of its own once the list has one.
  function addDownloadRoot() {
    const roots = [...(config.download_roots ?? [])];
    if (roots.length === 0 && config.completed_anime_path?.trim()) {
      roots.push({ name: "library", path: config.completed_anime_path.trim() });
    }
    roots.push({ name: "", path: "" });
    config.download_roots = roots;
  }
  function removeDownloadRoot(i: number) {
    config.download_roots = (config.download_roots ?? []).filter((_, k) => k !== i);
  }
  /** Moves a folder up (-1) or down (+1) in the fill order: the first is the highest priority. */
  function moveDownloadRoot(i: number, by: number) {
    const roots = [...(config.download_roots ?? [])];
    const j = i + by;
    if (j < 0 || j >= roots.length) return;
    [roots[i], roots[j]] = [roots[j], roots[i]];
    config.download_roots = roots;
  }
  $: rootNames = (config.download_roots ?? []).map((r) => r.name.trim());

  /** The state a Toggle's forwarded change event left its switch in. */
  function isOn(e: Event): boolean {
    return (e.target as HTMLInputElement).checked;
  }

  const ALL_STATUSES = ["CURRENT", "REPEATING", "PLANNING", "PAUSED", "DROPPED", "COMPLETED"];
  const ALL_MEDIA_STATUSES = ["RELEASING", "FINISHED", "CANCELLED", "HIATUS"];

  let config: Config = {
    anilist_usernames: [],
    mal_usernames: [],
    completed_anime_path: "",
    check_interval: 10,
    max_episodes_per_anime: 12,
    max_batch_torrent_size_gb: 100,
    max_episode_torrent_size_gb: 5,
    min_seeders: 3,
    max_search_pages: 5,
    nyaa_request_delay_seconds: 0,
    min_free_disk_gb: 10,
    episode_retry_limit: 5,
    max_concurrent_downloads: 3,
    delete_watched_episodes: true,
    watched_episodes_to_keep: 2,
    excluded_lists: [],
    rename_files_for_jellyfin: false,
    library_mode: "hardlink",
    auto_delete_downloads: false,
    incomplete_files: "part",
    incomplete_path: "",
    download_roots: [],
    incomplete_min_free_disk_gb: 10,
    episode_numbering: "season",
    disable_seeding: false,
    check_for_updates: true,
    download_statuses: ["CURRENT", "REPEATING"],
    download_media_statuses: ["RELEASING", "FINISHED"],
    delete_statuses: [],
    notifications: { webhooks: [], batch_window_seconds: 60 },
    priorities: {
      criteria_order: [],
      fansubs: [],
      resolutions: [],
      sources: [],
      codecs: [],
      audio: [],
      ignore_list: [],
    },
    torrent_client: "builtin",
    qbittorrent_url: "",
    qbittorrent_username: "",
    qbittorrent_password: "",
    qbittorrent_api_key: "",
    qbittorrent_category: "autoanimedownloader",
    qbittorrent_path_map: [],
    shoko_path_map: [],
    qbittorrent_manage_settings: false,
    qbittorrent_disable_seeding_upload_kib: 16,
    shoko_enabled: false,
    shoko_url: "",
    shoko_api_key: "",
    shoko_refresh_hours: 24,
    shoko_watch: true,
    shoko_full_resync_days: 30,
    shoko_full_packs: true,
  };

  // The Shoko group: the sign-in (the password is sent once and kept nowhere, not even here once
  // sent) and what the last sync read.
  let shokoUser = "";
  let shokoPassword = "";
  let shokoSigningIn = false;
  let shokoStatus: ShokoStatus | null = null;
  /** A Shoko import folder's kind in words: Shoko's own drop types are identifiers. */
  function shokoFolderKind(folder: ShokoImportFolder): string {
    if (folder.drop_source) return m.config_shoko_folder_drop_source();
    if (folder.drop_type === "Destination") return m.config_shoko_folder_drop_destination();
    return m.config_shoko_folder_plain();
  }
  // Read in the Library group too, while Shoko is on: each download folder says there whether it
  // is one of Shoko's import folders.
  $: if ((activeGroup === "shoko" || (activeGroup === "library" && config.shoko_enabled)) && !shokoStatus) loadShokoStatus();

  // Shoko's import folders (decisions/152): read from Shoko, never managed here. A download folder
  // is Shoko's when the server, through shoko_path_map, finds it in one of them; the answer is for
  // the saved folders, so an edited row waits for Save.
  $: shokoOn = !!config.shoko_enabled && !!shokoStatus?.signed_in;
  $: shokoRoots = shokoStatus?.download_roots ?? [];
  // The answers are an argument, not read from the closure: the row must redraw when they change.
  function rootInShoko(answers: ShokoDownloadFolder[], root: DownloadRoot): ShokoDownloadFolder | undefined {
    return answers.find((r) => r.name === root.name && r.path === root.path);
  }
  const trimSlash = (p: string) => p.replace(/[\\/]+$/, "");
  // Shoko's folders offered under Download Folders: not one a download folder already is, holds or
  // lies inside (download folders can't nest). With none listed, the library is the one download
  // folder, and it stays one after Shoko's.
  $: rootPaths = ((config.download_roots ?? []).length
    ? (config.download_roots ?? []).map((r) => trimSlash(r.path.trim()))
    : [trimSlash(config.completed_anime_path?.trim() ?? "")]).filter(Boolean);
  const inside = (a: string, b: string) => a === b || a.startsWith(b + "/") || a.startsWith(b + "\\");
  $: shokoAddable = shokoOn && config.library_mode === "direct"
    ? (shokoStatus?.import_folders ?? []).filter((f) => {
        const p = trimSlash(f.app_path);
        return !!p && !rootPaths.some((r) => inside(p, r) || inside(r, p));
      })
    : [];
  // With no download folders listed, the library is the one, and the server answers for it too.
  $: libraryShokoShown = shokoOn && config.library_mode === "direct" && !(config.download_roots ?? []).length;
  $: libraryInShoko = libraryShokoShown ? rootInShoko(shokoRoots, { name: "library", path: config.completed_anime_path ?? "" }) : undefined;
  function addShokoFolder(folder: ShokoImportFolder) {
    const roots = [...(config.download_roots ?? [])];
    // An empty list is the library alone: it stays a download folder, after Shoko's.
    const keepLibrary = roots.length === 0 && !!config.completed_anime_path?.trim();
    let name = folder.name.trim() || "shoko";
    for (let n = 2; roots.some((r) => r.name === name) || (keepLibrary && name === "library"); n++) name = `${folder.name.trim() || "shoko"} ${n}`;
    roots.push({ name, path: folder.app_path });
    if (keepLibrary) roots.push({ name: "library", path: config.completed_anime_path.trim() });
    config.download_roots = roots;
  }
  $: anyRootInShoko = shokoRoots.some((r) => r.in_shoko);

  function addShokoPathMapping() {
    config.shoko_path_map = [...(config.shoko_path_map ?? []), { app: "", shoko: "" }];
  }
  function removeShokoPathMapping(index: number) {
    config.shoko_path_map = (config.shoko_path_map ?? []).filter((_, k) => k !== index);
  }

  async function loadShokoStatus() {
    try {
      shokoStatus = await getShokoStatus();
    } catch {
      shokoStatus = null;
    }
  }

  async function signInToShoko() {
    shokoSigningIn = true;
    try {
      const url = (config.shoko_url ?? "").trim();
      await shokoLogin(url, shokoUser, shokoPassword);
      // The key is saved on the server: this copy must say so, or the next Save would send the old
      // (empty) one back and wipe it.
      config.shoko_api_key = SECRET_PLACEHOLDER;
      config.shoko_url = url.replace(/\/+$/, "");
      // Saved on the server already: the baseline takes the two fields too, so the page doesn't read
      // as having unsaved changes for them (decisions/129). Other edits stay unsaved.
      if (baseline) {
        const saved = JSON.parse(baseline);
        saved.shoko_api_key = config.shoko_api_key;
        saved.shoko_url = config.shoko_url;
        baseline = snapshot(saved);
      }
      shokoPassword = "";
      toast.success(m.config_shoko_login_ok());
      await loadShokoStatus();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      shokoSigningIn = false;
    }
  }

  async function fullShokoSync() {
    try {
      await shokoSync(true);
      toast.success(m.config_shoko_sync_started());
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  // The connection test of the "Torrent client" group: the result of the last run, or its error.
  let qbitTesting = false;
  let qbitTestResult: TorrentClientTestResult | null = null;
  let qbitTestError = "";

  async function runQBitTest() {
    qbitTesting = true;
    qbitTestResult = null;
    qbitTestError = "";
    try {
      qbitTestResult = await testTorrentClient(config);
    } catch (err) {
      qbitTestError = err instanceof Error ? err.message : String(err);
    } finally {
      qbitTesting = false;
    }
  }

  function addPathMapping() {
    config.qbittorrent_path_map = [...(config.qbittorrent_path_map ?? []), { app: "", qbittorrent: "" }];
  }

  function removePathMapping(index: number) {
    config.qbittorrent_path_map = (config.qbittorrent_path_map ?? []).filter((_, i) => i !== index);
  }

  // A status can't be in "download" and "delete" at the same time — turning one on always turns the
  // other off. Pre-existing rule, kept verbatim.
  function toggleDownloadStatus(status: string) {
    const active = (config.download_statuses ?? []).includes(status);
    if (active) {
      config.download_statuses = (config.download_statuses ?? []).filter(s => s !== status);
    } else {
      config.download_statuses = [...(config.download_statuses ?? []), status];
      config.delete_statuses = (config.delete_statuses ?? []).filter(s => s !== status);
    }
  }

  function toggleDownloadMediaStatus(status: string) {
    const active = (config.download_media_statuses ?? []).includes(status);
    if (active) {
      config.download_media_statuses = (config.download_media_statuses ?? []).filter(s => s !== status);
    } else {
      config.download_media_statuses = [...(config.download_media_statuses ?? []), status];
    }
  }

  function toggleDeleteStatus(status: string) {
    const active = (config.delete_statuses ?? []).includes(status);
    if (active) {
      config.delete_statuses = (config.delete_statuses ?? []).filter(s => s !== status);
    } else {
      config.delete_statuses = [...(config.delete_statuses ?? []), status];
      config.download_statuses = (config.download_statuses ?? []).filter(s => s !== status);
    }
  }

  function statusPillClass(active: boolean, variant: "accent" | "danger"): string {
    if (!active) return "border-default bg-control text-subtle hover:text-body";
    return variant === "danger"
      ? "border-danger-tint/28 bg-danger-tint/12 text-danger"
      : "border-accent-tint/28 bg-accent-tint/12 text-accent";
  }

  let loading = true;
  /** The load failed: the form must NOT render. Its starting values are not the user's config, and
   * saving them would overwrite it. */
  let loadFailed = false;
  let saving = false;
  let showMissingConfigBanner = false;
  // Snapshot of what was loaded (or last saved); "" = nothing loaded, never dirty. Taken AFTER the
  // load's normalization below, or the legacy anilist_username fill-in would read as an edit.
  let baseline = "";

  // The download folders as the save sends them. An empty Keep Free is "use Min Free Disk Space":
  // absent, never "" (the daemon reads a number); a cleared field must not read as an edit either.
  function savedRoots(roots: DownloadRoot[] | undefined): DownloadRoot[] {
    return (roots ?? []).map(({ min_free_gb, ...root }) => ({
      ...root,
      ...(typeof min_free_gb === "number" && Number.isFinite(min_free_gb) ? { min_free_gb } : {}),
    }));
  }
  const configSnapshot = (c: Config) => snapshot({ ...c, download_roots: savedRoots(c.download_roots) });

  $: setUnsaved("config", baseline !== "" && configSnapshot(config) !== baseline);
  onDestroy(() => setUnsaved("config", false));

  /**
   * The app is a hash SPA, so the query can be in `?a=b#/config` (rare) or after the `?` INSIDE the
   * hash (`#/config?a=b`, the normal path). The params are resolved ONCE and every parameter comes
   * from there: with one `return` per branch, each new parameter would have to be read in both
   * places, and the two could diverge.
   */
  function resolveParams(): URLSearchParams | null {
    if (typeof window === "undefined") return null;
    if (window.location.search) return new URLSearchParams(window.location.search);
    const hashParts = window.location.hash.split("?");
    return hashParts.length > 1 ? new URLSearchParams(hashParts[1]) : null;
  }

  // The card also disappears when the user marks the three steps by hand, so bringing it back means
  // clearing both things — only resetting the dismissal would leave the button with no visible
  // effect.
  $: onboardingHidden =
    $onboardingDismissed || ONBOARDING_STEP_IDS.every((id) => $onboardingDone.includes(id));

  function restoreOnboarding() {
    onboardingDismissed.set(false);
    onboardingDone.reset();
  }

  function checkQueryParams() {
    const params = resolveParams();
    if (!params) return;

    showMissingConfigBanner = params.has("missingConfig");

    // Validation uses the `groups` array the screen already builds — a second list of ids would
    // drift from it. An unknown value is ignored and `activeGroup` stays on the default "library":
    // without the guard, an old or mistyped link would leave the screen on a group that renders
    // nothing.
    //
    // No conflict with validation: `firstValidationError()` only touches `activeGroup` on the Save
    // click, and this runs in `onMount`.
    const group = params.get("group");
    if (group && groups.some((g) => g.id === group)) activeGroup = group as GroupId;
  }

  async function loadConfig() {
    try {
      loading = true;
      loadFailed = false;
      const data = await getConfig();
      config = { ...data, anilist_usernames: data.anilist_usernames ?? [], mal_usernames: data.mal_usernames ?? [] };
      if (config.anilist_username && (config.anilist_usernames ?? []).length === 0) {
        config.anilist_usernames = [config.anilist_username];
      }
      if (!config.notifications) config.notifications = { webhooks: [], batch_window_seconds: 0 };
      if (!Array.isArray(config.qbittorrent_path_map)) config.qbittorrent_path_map = [];
      if (!Array.isArray(config.shoko_path_map)) config.shoko_path_map = [];
      if (!Array.isArray(config.notifications.webhooks)) config.notifications.webhooks = [];
      baseline = configSnapshot(config);
    } catch (err) {
      loadFailed = true;
      toast.error(err instanceof Error ? err.message : m.config_error_load());
    } finally {
      loading = false;
    }
  }

  /**
   * The same six validations as before, in the same order — only now each one knows which group the
   * field it rejects lives in. That's a requirement of the new layout, not a new rule: with one
   * group visible at a time, a toast saying "folder is required" while the user looks at
   * "Automation" couldn't be acted on. The failing validation brings its group to the screen.
   *
   * They became a LIST (it used to be a chain of `if`) because the screen now uses them for two
   * things: the Save toast, as always, and the "needs filling" mark in the side index. Rewriting
   * the conditions in the second place would make the mark lie the first time a rule changed — the
   * index has to say exactly what blocks Save, nothing more.
   *
   * `message` holds the REFERENCE to the paraglide function, not the text: that way the message is
   * resolved in the language active at click time, as it was when each `if` called it.
   */
  $: requiredChecks = [
    // A list account (AniList or MyAnimeList) is NOT required: with standalone animes (#/add) the
    // app works entirely without any list, and the backend stopped validating it. A requirement
    // only in the frontend would be a rule the server doesn't know.
    {
      group: "library" as GroupId,
      ok: !!config.completed_anime_path?.trim(),
      message: m.config_val_completed_path,
    },
    {
      group: "library" as GroupId,
      // Direct mode only, as on the server; the server also checks the folders exist.
      ok:
        config.library_mode !== "direct" ||
        ((config.download_roots ?? []).every((r) => r.name.trim() && r.path.trim()) && new Set(rootNames).size === rootNames.length),
      message: m.config_val_download_roots,
    },
    {
      group: "downloads" as GroupId,
      ok: config.check_interval > 0,
      message: m.config_val_interval,
    },
    {
      group: "downloads" as GroupId,
      ok: config.max_episodes_per_anime >= 0,
      message: m.config_val_max_episodes,
    },
    {
      group: "search" as GroupId,
      ok: config.episode_retry_limit >= 0,
      message: m.config_val_retry,
    },
    {
      group: "downloads" as GroupId,
      ok: config.max_concurrent_downloads >= 0,
      message: m.config_val_max_concurrent,
    },
    {
      group: "downloads" as GroupId,
      ok: !(config.delete_watched_episodes && config.watched_episodes_to_keep < 0),
      message: m.config_val_watched_keep,
    },
    {
      group: "search" as GroupId,
      ok: config.max_batch_torrent_size_gb >= 0 && config.max_episode_torrent_size_gb >= 0,
      message: m.config_val_torrent_size,
    },
    {
      group: "search" as GroupId,
      ok: config.min_seeders >= 0,
      message: m.config_val_min_seeders,
    },
    {
      group: "search" as GroupId,
      ok: config.max_search_pages >= 0,
      message: m.config_val_max_search_pages,
    },
    {
      group: "search" as GroupId,
      ok: config.nyaa_request_delay_seconds >= 0,
      message: m.config_val_nyaa_request_delay,
    },
    {
      group: "library" as GroupId,
      ok: config.min_free_disk_gb >= 0,
      message: m.config_val_min_free_disk,
    },
    {
      group: "library" as GroupId,
      ok: (config.incomplete_min_free_disk_gb ?? 0) >= 0,
      message: m.config_val_incomplete_min_free_disk,
    },
    {
      group: "client" as GroupId,
      ok: config.torrent_client !== "qbittorrent" || !!config.qbittorrent_url?.trim(),
      message: m.config_val_qbittorrent_url,
    },
    {
      group: "shoko" as GroupId,
      ok: !config.shoko_enabled || (config.shoko_refresh_hours ?? 0) >= 1,
      message: m.config_val_shoko_refresh,
    },
    {
      group: "shoko" as GroupId,
      ok: (config.shoko_full_resync_days ?? 0) >= 0,
      message: m.config_val_shoko_full_resync,
    },
    // A download folder that is one of Shoko's (decisions/152), as saved: Shoko never sees a
    // half-written file. The server checks the folders being saved.
    {
      group: "downloads" as GroupId,
      ok: !config.shoko_enabled || !anyRootInShoko || config.incomplete_files !== "in_place" || !!config.incomplete_path?.trim(),
      message: m.config_val_shoko_folder_in_place,
    },
    {
      group: "downloads" as GroupId,
      // qBittorrent moves a staged torrent in under its final names (decisions/145).
      ok: !config.shoko_enabled || !anyRootInShoko || config.torrent_client !== "qbittorrent" || !config.incomplete_path?.trim(),
      message: m.config_val_shoko_folder_qbit_staging,
    },
    {
      group: "downloads" as GroupId,
      // Only while disable seeding is on, the one time the field shows; missing reads as 0, as on the server.
      ok: config.torrent_client !== "qbittorrent" || !config.disable_seeding || (config.qbittorrent_disable_seeding_upload_kib ?? 0) >= 1,
      message: m.config_val_qbittorrent_disable_seeding_upload,
    },
  ];

  /** Groups with something pending — feeds the dot in the side index. */
  $: pendingGroups = new Set(requiredChecks.filter((c) => !c.ok).map((c) => c.group));

  function firstValidationError(): { message: string; group: GroupId } | null {
    const failed = requiredChecks.find((c) => !c.ok);
    return failed ? { message: failed.message(), group: failed.group } : null;
  }

  async function saveConfig() {
    try {
      saving = true;

      const invalid = firstValidationError();
      if (invalid) {
        activeGroup = invalid.group;
        throw new Error(invalid.message);
      }

      config.download_roots = savedRoots(config.download_roots);
      const result = await updateConfig(config);
      baseline = configSnapshot(config);
      // Which download folders are Shoko's follows the saved folders and path map.
      if (shokoStatus) void loadShokoStatus();
      toast.success(result?.restart_required ? m.config_restart_required() : m.config_saved());
    } catch (err) {
      toast.error(err instanceof Error ? err.message : m.config_error_save());
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    checkQueryParams();
    loadConfig();
  });
</script>

<div class="space-y-4.5">
  <div>
    <h1 class="text-screen-title text-heading">{T && T.title}</h1>
    <p class="mt-0.5 text-caption text-subtle">{T && T.subtitle}</p>
  </div>

  {#if showMissingConfigBanner}
    <div
      role="alert"
      class="flex items-center gap-2 rounded-field border border-warn-tint/28 bg-warn-tint/12 px-3.5 py-2.5 text-copy text-warn"
    >
      {T && T.missingBanner}
    </div>
  {/if}

  {#if loading}
    <Loading message={T && T.loading || ""} />
  {:else if loadFailed}
    <div class="flex flex-wrap items-center gap-3 rounded-card border border-default bg-card p-4.5" role="alert">
      <p class="text-copy text-body">{m.config_error_load()}</p>
      <Button variant="ghost" on:click={loadConfig}>{m.config_btn_retry_load()}</Button>
    </div>
  {:else}
    <form on:submit|preventDefault={saveConfig} class="space-y-4">
      <!-- Asterisk legend. Without it the `*` is an implicit convention; with one group at a time,
           it's worth saying in text what the mark means. -->
      <p class="text-caption text-subtle">
        <span class="text-danger" aria-hidden="true">*</span>
        {T && T.requiredLegend}
      </p>

      <div class="grid gap-3.5 md:grid-cols-[196px_1fr] md:items-start">
        <!-- Index: a 196px column on desktop; on mobile the items WRAP, they don't scroll. Same
             decision (and same reason) as the filter pills of `DownloadsToolbar.svelte`: at 390px
             the strip had 358px of space for 644px of content, so half the options — including
             "Torrent search" and the two exit links — only showed up after dragging. `shrink-0`
             keeps the label whole instead of squeezing it.

             The dividers are `w-full`, so the wrap happens AT THEM: the three rows that come out of
             it are the screen's architecture — everyday groups, the advanced group, and what leaves
             the screen. On desktop the column says this implicitly; here it's explicit.

             The pending dot goes HERE because only one group is on screen at a time: the asterisk
             on the field covers the open group, and this dot is what tells the user something is
             still missing in the other three. The sr-only text goes into the button's accessible
             name ("Lists, required configuration missing"); the dot itself is decorative. -->
        <nav
          aria-label={(T && T.navLabel) || ""}
          class="flex flex-wrap gap-1 md:flex-col"
        >
          {#each groups as group (group.id)}
            {#if group.advanced}
              <!-- `w-full`: besides separating, it's what pushes "Torrent search" to the next row
                   on mobile. One axis at both widths. -->
              <div class="my-1 w-full border-t border-divider md:my-1.5" aria-hidden="true"></div>
            {/if}
            <button
              type="button"
              aria-current={activeGroup === group.id ? "true" : undefined}
              on:click={() => (activeGroup = group.id)}
              class="flex shrink-0 items-center gap-1.5 rounded-field px-3 py-2 text-left text-copy transition-colors md:w-full {activeGroup ===
              group.id
                ? 'bg-accent-tint/16 font-bold text-nav-active'
                : 'font-semibold text-subtle hover:text-body'}"
            >
              {group.label}
              {#if pendingGroups.has(group.id)}
                <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-warn" aria-hidden="true"></span>
                <span class="sr-only">{T && T.groupMissing}</span>
              {/if}
            </button>
          {/each}

          <!-- Divider: what comes below LEAVES the screen instead of switching the visible group. -->
          <div class="my-1 w-full border-t border-divider md:my-1.5" aria-hidden="true"></div>

          <a
            href="#/priorities"
            class="flex shrink-0 items-center gap-1.5 rounded-field px-3 py-2 text-left text-copy font-semibold text-subtle transition-colors hover:text-body md:w-full"
          >
            {T && T.linkPriorities}
            <ArrowUpRight size={14} strokeWidth={2.5} class="shrink-0 opacity-70" aria-hidden="true" />
          </a>
          <a
            href="#/notifications"
            class="flex shrink-0 items-center gap-1.5 rounded-field px-3 py-2 text-left text-copy font-semibold text-subtle transition-colors hover:text-body md:w-full"
          >
            {T && T.linkNotifications}
            <ArrowUpRight size={14} strokeWidth={2.5} class="shrink-0 opacity-70" aria-hidden="true" />
          </a>
        </nav>

        <!-- `divide-y` draws the 1px dividers BETWEEN the fields, with no leftover border on the
             first or the last row of the card. -->
        <div class="divide-y divide-divider rounded-card border border-default bg-card">
          {#if activeGroup === "anilist"}
            <div class="p-4.5">
              <ChipsInput
                id="anilist_usernames"
                bind:values={config.anilist_usernames}
                label={(T && T.labelUsername) || ""}
                hint={(T && T.hintAnilistUsernames) || ""}
                placeholder={(T && T.chipsPlaceholder) || ""}
                removeLabel={(item) => m.config_chips_remove({ item })}
              />
            </div>

            <div class="p-4.5">
              <ChipsInput
                id="mal_usernames"
                bind:values={config.mal_usernames}
                label={(T && T.labelMalUsernames) || ""}
                hint={(T && T.hintMalUsernames) || ""}
                placeholder={(T && T.chipsPlaceholder) || ""}
                removeLabel={(item) => m.config_chips_remove({ item })}
              />
            </div>

            <div class="p-4.5">
              <ChipsInput
                id="excluded_lists"
                bind:values={config.excluded_lists}
                label={(T && T.labelExcludedList) || ""}
                hint={(T && T.hintExcludedList) || ""}
                placeholder={(T && T.chipsPlaceholder) || ""}
                removeLabel={(item) => m.config_chips_remove({ item })}
              />
            </div>

            <fieldset class="p-4.5">
              <legend class="float-left w-full text-[14.5px] font-bold text-heading">{T && T.labelDownloadStatuses}</legend>
              <p class="text-caption text-subtle">{T && T.hintDownloadStatuses}</p>
              <div class="mt-2.5 flex flex-wrap gap-2">
                {#each ALL_STATUSES as status}
                  {@const active = (config.download_statuses ?? []).includes(status)}
                  <button
                    type="button"
                    aria-pressed={active}
                    on:click={() => toggleDownloadStatus(status)}
                    title={status}
                    class="inline-flex items-center gap-1 rounded-pill border px-3 py-1.5 text-caption font-semibold transition-colors {statusPillClass(
                      active,
                      'accent'
                    )}"
                  >
                    {#if active}<Check size={13} strokeWidth={3} />{/if}
                    {T ? T.statusLabels[status] : status}
                  </button>
                {/each}
              </div>
            </fieldset>

            <fieldset class="p-4.5">
              <legend class="float-left w-full text-[14.5px] font-bold text-heading">{T && T.labelDownloadMediaStatuses}</legend>
              <p class="text-caption text-subtle">{T && T.hintDownloadMediaStatuses}</p>
              <div class="mt-2.5 flex flex-wrap gap-2">
                {#each ALL_MEDIA_STATUSES as status}
                  {@const active = (config.download_media_statuses ?? []).includes(status)}
                  <button
                    type="button"
                    aria-pressed={active}
                    on:click={() => toggleDownloadMediaStatus(status)}
                    title={status}
                    class="inline-flex items-center gap-1 rounded-pill border px-3 py-1.5 text-caption font-semibold transition-colors {statusPillClass(
                      active,
                      'accent'
                    )}"
                  >
                    {#if active}<Check size={13} strokeWidth={3} />{/if}
                    {T ? T.statusLabels[status] : status}
                  </button>
                {/each}
              </div>
            </fieldset>

            <fieldset class="p-4.5">
              <legend class="float-left w-full text-[14.5px] font-bold text-heading">{T && T.labelDeleteStatuses}</legend>
              <p class="text-caption text-subtle">{T && T.hintDeleteStatuses}</p>
              <div class="mt-2.5 flex flex-wrap gap-2">
                {#each ALL_STATUSES as status}
                  {@const active = (config.delete_statuses ?? []).includes(status)}
                  <button
                    type="button"
                    aria-pressed={active}
                    on:click={() => toggleDeleteStatus(status)}
                    title={status}
                    class="inline-flex items-center gap-1 rounded-pill border px-3 py-1.5 text-caption font-semibold transition-colors {statusPillClass(
                      active,
                      'danger'
                    )}"
                  >
                    {#if active}<Check size={13} strokeWidth={3} />{/if}
                    {T ? T.statusLabels[status] : status}
                  </button>
                {/each}
              </div>
            </fieldset>
          {/if}

          {#if activeGroup === "library"}
            <div class="p-4.5">
              <Input
                id="completed_anime_path"
                label={T && T.labelCompletedPath || ""}
                subtitle={(T && (config.library_mode === "direct" ? T.hintCompletedPathDirect : T.hintCompletedPath)) || ""}
                type="text"
                bind:value={config.completed_anime_path}
                placeholder="/path/to/completed"
                required={true}
              />
              {#if libraryShokoShown}
                <p class="mt-2 text-caption {libraryInShoko?.in_shoko ? 'text-accent' : 'text-subtle'}" data-testid="library-shoko">
                  {$locale && (libraryInShoko ? (libraryInShoko.in_shoko ? m.config_root_in_shoko({ folder: libraryInShoko.folder_name ?? "" }) : m.config_root_not_in_shoko()) : m.config_root_shoko_after_save())}
                </p>
              {/if}
            </div>

            <!-- The hint always shows: it's precisely what helps decide whether to turn the switch
                 on, so hiding it behind the on state hides the useful information. `Toggle` has no
                 hint prop, hence the sibling <p>. -->
            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="rename_files_for_jellyfin"
                bind:checked={config.rename_files_for_jellyfin}
                label={(T && T.labelRenameJellyfin) || ""}
                inline={true}
              />
              <p class="text-caption text-subtle">{T && T.hintRenameJellyfin}</p>
            </div>

            <!-- Direct library mode. The options are two-valued strings in config.json, drawn as
                 toggles (no select component exists here): `checked` is one-way and on:change writes
                 the string back. Auto delete shows in both modes: it governs the torrents added in
                 direct mode even after a switch back to hardlink mode. The two below it only shape
                 new direct-mode torrents, so they hide in hardlink mode. -->
            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="library_mode"
                checked={config.library_mode === "direct"}
                on:change={(e) => (config.library_mode = isOn(e) ? "direct" : "hardlink")}
                label={(T && T.labelDirectMode) || ""}
                inline={true}
              />
              <p class="text-caption text-subtle">{T && T.hintDirectMode}</p>
            </div>

            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="auto_delete_downloads"
                bind:checked={config.auto_delete_downloads}
                label={(T && T.labelAutoDeleteDownloads) || ""}
                inline={true}
              />
              <p class="text-caption text-subtle">{T && T.hintAutoDeleteDownloads}</p>
            </div>

            {#if config.library_mode === "direct"}

              <div class="space-y-1.5 p-4.5">
                <Toggle
                  id="incomplete_files"
                  checked={config.incomplete_files !== "in_place"}
                  on:change={(e) => (config.incomplete_files = isOn(e) ? "part" : "in_place")}
                  label={(T && T.labelPartFiles) || ""}
                  inline={true}
                />
                <p class="text-caption text-subtle">{T && T.hintPartFiles}</p>
              </div>

              <!-- Staging (decisions/141): downloads on a fast drive, moved into the library
                   when finished. The daemon validates the folder on save (it must exist; the app
                   never creates it). -->
              <div class="p-4.5">
                <Input
                  id="incomplete_path"
                  label={(T && T.labelIncompletePath) || ""}
                  subtitle={(T && T.hintIncompletePath) || ""}
                  type="text"
                  bind:value={config.incomplete_path}
                  placeholder="/mnt/nvme/aad-incomplete"
                />
              </div>

              <!-- Several download folders, filled in order (decisions/154). The library is a root
                   only while the list is empty, so the first add lists it too. -->
              <div class="space-y-2.5 p-4.5" data-testid="download-roots">
                <p class="text-copy font-semibold text-heading">{$locale && m.config_label_download_roots()}</p>
                <p class="text-caption text-subtle">{$locale && m.config_hint_download_roots()}</p>
                {#each config.download_roots ?? [] as _, i}
                  <div class="flex flex-col gap-2 sm:flex-row sm:items-end" data-testid="download-root">
                    <div class="sm:w-40">
                      <Input id={`download_root_name_${i}`} label={$locale && m.config_label_root_name()} bind:value={config.download_roots[i].name} />
                    </div>
                    <div class="min-w-0 flex-1">
                      <Input id={`download_root_path_${i}`} label={$locale && m.config_label_root_path()} bind:value={config.download_roots[i].path} placeholder="/mnt/disk2/anime" />
                    </div>
                    <div class="sm:w-32">
                      <Input
                        id={`download_root_min_free_${i}`}
                        type="number"
                        min="0"
                        label={$locale && m.config_label_root_min_free()}
                        bind:value={config.download_roots[i].min_free_gb}
                        placeholder={String(config.min_free_disk_gb ?? 0)}
                      />
                    </div>
                    {#if shokoOn}
                      {@const inShoko = rootInShoko(shokoRoots, config.download_roots[i])}
                      <p class="text-caption sm:pb-2.5 {inShoko?.in_shoko ? 'text-accent' : 'text-subtle'}" data-testid={`download_root_shoko_${i}`}>
                        {$locale && (inShoko ? (inShoko.in_shoko ? m.config_root_in_shoko({ folder: inShoko.folder_name ?? "" }) : m.config_root_not_in_shoko()) : m.config_root_shoko_after_save())}
                      </p>
                    {/if}
                    <div class="flex gap-1.5">
                      <Button variant="ghost" disabled={i === 0} on:click={() => moveDownloadRoot(i, -1)}>
                        <span aria-hidden="true">↑</span><span class="sr-only">{$locale && m.config_btn_root_up()}</span>
                      </Button>
                      <Button variant="ghost" disabled={i === (config.download_roots ?? []).length - 1} on:click={() => moveDownloadRoot(i, 1)}>
                        <span aria-hidden="true">↓</span><span class="sr-only">{$locale && m.config_btn_root_down()}</span>
                      </Button>
                      <Button variant="ghost" on:click={() => removeDownloadRoot(i)}>{$locale && m.config_btn_remove_root()}</Button>
                    </div>
                  </div>
                {/each}
                <Button variant="ghost" on:click={addDownloadRoot}>{$locale && m.config_btn_add_root()}</Button>
                {#if shokoAddable.length}
                  <div class="space-y-1.5" data-testid="shoko-addable">
                    <p class="text-caption text-body">{$locale && m.config_shoko_add_from()}</p>
                    <div class="flex flex-wrap gap-2">
                      {#each shokoAddable as folder (folder.id)}
                        <button
                          type="button"
                          class="rounded-card border border-neutral/45 bg-control px-3 py-1 text-left font-mono text-caption text-body break-all"
                          on:click={() => addShokoFolder(folder)}
                        >{folder.name} · {folder.app_path}</button>
                      {/each}
                    </div>
                  </div>
                {/if}
              </div>

              {#if config.incomplete_path}
                <div class="p-4.5">
                  <Input
                    id="incomplete_min_free_disk_gb"
                    label={(T && T.labelIncompleteMinFree) || ""}
                    subtitle={(T && T.hintIncompleteMinFree) || ""}
                    type="number"
                    bind:value={config.incomplete_min_free_disk_gb}
                    min="0"
                    inline={true}
                    suffix="GiB"
                  />
                </div>
              {/if}

              <div class="space-y-1.5 p-4.5">
                <Toggle
                  id="episode_numbering"
                  checked={config.episode_numbering !== "entry"}
                  on:change={(e) => (config.episode_numbering = isOn(e) ? "season" : "entry")}
                  label={(T && T.labelSeasonNumbering) || ""}
                  inline={true}
                />
                <p class="text-caption text-subtle">{T && T.hintSeasonNumbering}</p>
              </div>
            {/if}

            <div class="p-4.5">
              <Input
                id="min_free_disk_gb"
                label={T && T.labelMinFreeDisk || ""}
                subtitle={T && T.hintMinFreeDisk || ""}
                type="number"
                bind:value={config.min_free_disk_gb}
                min="0"
                inline={true}
                suffix="GiB"
              />
            </div>

            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="check_for_updates"
                bind:checked={config.check_for_updates}
                label={(T && T.labelCheckForUpdates) || ""}
                inline={true}
              />
              <p class="text-caption text-subtle">{T && T.hintCheckForUpdates}</p>
            </div>

            <!-- The way back to the first-steps card. It's NOT a config field: it doesn't go into
                 `requiredChecks` nor into the PUT body — it's a UI preference, per browser. The
                 divider comes for free from the container's `divide-y`. `Input`/`Toggle` don't fit
                 here because both `bind:` to a `config` field, and there's no field.

                 The disabled state is about the DISMISSAL, not about the card being visible: with
                 the three items green the card doesn't show anyway, and an enabled button here
                 would be a click with no visible effect. -->
            <div class="flex flex-wrap items-center justify-between gap-3 p-4.5">
              <div class="min-w-0">
                <p class="text-[14.5px] font-bold text-heading">{T && T.onboardingRestoreLabel}</p>
                <p class="text-caption text-subtle">{T && T.onboardingRestoreHint}</p>
              </div>
              <Button
                variant="ghost"
                disabled={!onboardingHidden}
                on:click={restoreOnboarding}
              >
                {T && T.onboardingRestoreButton}
              </Button>
            </div>
          {/if}

          {#if activeGroup === "downloads"}
            <div class="p-4.5">
              <Input
                id="check_interval"
                label={T && T.labelCheckInterval || ""}
                type="number"
                bind:value={config.check_interval}
                min="1"
                required={true}
                inline={true}
                suffix="min"
              />
            </div>
            <div class="space-y-1.5 p-4.5">
              <Input
                id="max_concurrent_downloads"
                label={T && T.labelMaxConcurrent || ""}
                type="number"
                bind:value={config.max_concurrent_downloads}
                min="0"
                required={true}
                inline={true}
              />
              <p class="text-caption text-subtle">{T && T.hintMaxConcurrent}</p>
              {#if config.torrent_client === "qbittorrent"}
                <p class="text-caption text-subtle" data-testid="max-concurrent-qbittorrent">
                  {config.qbittorrent_manage_settings ? (T && T.hintMaxConcurrentQBitManaged) : (T && T.hintMaxConcurrentQBitOwn)}
                </p>
              {/if}
            </div>
            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="disable_seeding"
                bind:checked={config.disable_seeding}
                label={(T && T.labelDisableSeeding) || ""}
                inline={true}
              />
              <p class="text-caption text-subtle">{T && T.hintDisableSeeding}</p>
              {#if config.torrent_client === "qbittorrent"}
                <p class="text-caption text-subtle" data-testid="disable-seeding-qbittorrent">{T && T.hintDisableSeedingQBit}</p>
                {#if config.disable_seeding}
                  <Input
                    id="qbittorrent_disable_seeding_upload_kib"
                    label={(T && T.labelQBitUploadFloor) || ""}
                    subtitle={(T && T.hintQBitUploadFloor) || ""}
                    type="number"
                    bind:value={config.qbittorrent_disable_seeding_upload_kib}
                    min="1"
                    inline={true}
                    suffix="KiB/s"
                  />
                {/if}
              {/if}
            </div>
            <div class="p-4.5">
              <Input
                id="max_episodes_per_anime"
                label={T && T.labelMaxEpisodes || ""}
                subtitle={T && T.hintMaxEpisodes || ""}
                type="number"
                bind:value={config.max_episodes_per_anime}
                min="0"
                required={true}
                inline={true}
              />
            </div>

            <!-- No divider between the switch and the amount: the absence of the divider is what
                 says the second field depends on the first. -->
            <div class="space-y-3 p-4.5">
              <Toggle
                id="delete_watched_episodes"
                bind:checked={config.delete_watched_episodes}
                label={(T && T.labelDeleteWatched) || ""}
                inline={true}
              />
              {#if config.delete_watched_episodes}
                <Input
                  id="watched_episodes_to_keep"
                  label={T && T.labelWatchedKeep || ""}
                  subtitle={T && T.hintWatchedKeep || ""}
                  type="number"
                  bind:value={config.watched_episodes_to_keep}
                  min="0"
                  inline={true}
                />
              {/if}
            </div>
          {/if}

          {#if activeGroup === "search"}
            <div class="p-4.5">
              <Input
                id="min_seeders"
                label={T && T.labelMinSeeders || ""}
                subtitle={T && T.hintMinSeeders || ""}
                type="number"
                bind:value={config.min_seeders}
                min="0"
                inline={true}
              />
            </div>

            <div class="p-4.5">
              <Input
                id="max_search_pages"
                label={T && T.labelMaxSearchPages || ""}
                subtitle={T && T.hintMaxSearchPages || ""}
                type="number"
                bind:value={config.max_search_pages}
                min="1"
                inline={true}
              />
            </div>

            <div class="p-4.5">
              <Input
                id="nyaa_request_delay_seconds"
                label={T && T.labelNyaaRequestDelay || ""}
                subtitle={T && T.hintNyaaRequestDelay || ""}
                type="number"
                bind:value={config.nyaa_request_delay_seconds}
                min="0"
                step="0.1"
                inline={true}
              />
            </div>

            <div class="p-4.5">
              <Input
                id="episode_retry_limit"
                label={T && T.labelRetryLimit || ""}
                type="number"
                bind:value={config.episode_retry_limit}
                min="0"
                required={true}
                inline={true}
              />
            </div>

            <div class="p-4.5">
              <Input
                id="max_batch_torrent_size_gb"
                label={T && T.labelMaxBatchSize || ""}
                subtitle={T && T.hintMaxBatchSize || ""}
                type="number"
                bind:value={config.max_batch_torrent_size_gb}
                min="0"
                step="0.1"
                inline={true}
                suffix="GiB"
              />
            </div>

            <div class="p-4.5">
              <Input
                id="max_episode_torrent_size_gb"
                label={T && T.labelMaxEpisodeSize || ""}
                subtitle={T && T.hintMaxEpisodeSize || ""}
                type="number"
                bind:value={config.max_episode_torrent_size_gb}
                min="0"
                step="0.1"
                inline={true}
                suffix="GiB"
              />
            </div>
          {/if}

          {#if activeGroup === "shoko"}
            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="shoko_enabled"
                bind:checked={config.shoko_enabled}
                label={$locale && m.config_label_shoko_enabled()}
                inline={true}
              />
              <p class="text-caption text-subtle">{$locale && m.config_hint_shoko_enabled()}</p>
            </div>
            <div class="p-4.5">
              <Input
                id="shoko_url"
                label={$locale && m.config_label_shoko_url()}
                subtitle={$locale && m.config_hint_shoko_url()}
                bind:value={config.shoko_url}
                placeholder="http://127.0.0.1:8111"
              />
            </div>
            <div class="space-y-3 p-4.5">
              <Input id="shoko_username" label={$locale && m.config_label_shoko_username()} bind:value={shokoUser} />
              <Input
                id="shoko_password"
                type="password"
                label={$locale && m.config_label_shoko_password()}
                subtitle={$locale && m.config_hint_shoko_password()}
                bind:value={shokoPassword}
              />
              <Button variant="ghost" disabled={shokoSigningIn || !config.shoko_url?.trim() || !shokoUser} on:click={signInToShoko}>
                {shokoSigningIn ? ($locale && m.config_shoko_signing_in()) : ($locale && m.config_btn_shoko_login())}
              </Button>
              <p class="text-caption text-body" data-testid="shoko-status">
                {#if shokoStatus?.signed_in}
                  {$locale && m.config_shoko_signed_in({ url: shokoStatus.url ?? "" })}
                  {#if shokoStatus.last_sync && !shokoStatus.last_sync.startsWith("0001")}
                    {$locale && m.config_shoko_last_sync({ when: new Date(shokoStatus.last_sync).toLocaleString(), series: String(shokoStatus.series), episodes: String(shokoStatus.owned_episodes) })}
                  {:else}
                    {$locale && m.config_shoko_never_synced()}
                  {/if}
                {:else}
                  {$locale && m.config_shoko_not_signed_in()}
                {/if}
              </p>
              {#if shokoStatus?.last_error}
                <p class="text-caption text-danger">{$locale && m.config_shoko_last_error({ error: shokoStatus.last_error })}</p>
              {/if}
            </div>
            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="shoko_watch"
                bind:checked={config.shoko_watch}
                label={$locale && m.config_label_shoko_watch()}
                inline={true}
              />
              <p class="text-caption text-subtle">{$locale && m.config_hint_shoko_watch()}</p>
              {#if config.shoko_enabled && config.shoko_watch && shokoStatus?.signed_in}
                <p class="text-caption {shokoStatus.watching ? 'text-body' : 'text-danger'}" data-testid="shoko-watch-status">
                  {#if shokoStatus.watching}
                    {$locale && m.config_shoko_watching()}
                  {:else}
                    {$locale && m.config_shoko_not_watching({ error: shokoStatus.watch_error ?? "" })}
                  {/if}
                </p>
              {/if}
            </div>
            <div class="p-4.5">
              <Input
                id="shoko_refresh_hours"
                label={$locale && m.config_label_shoko_refresh()}
                subtitle={$locale && m.config_hint_shoko_refresh()}
                type="number"
                bind:value={config.shoko_refresh_hours}
                min="1"
                inline={true}
                suffix="h"
              />
            </div>
            <div class="p-4.5">
              <Input
                id="shoko_full_resync_days"
                label={$locale && m.config_label_shoko_full_resync()}
                subtitle={$locale && m.config_hint_shoko_full_resync()}
                type="number"
                bind:value={config.shoko_full_resync_days}
                min="0"
                inline={true}
                suffix={$locale && m.config_suffix_days()}
              />
            </div>
            <div class="space-y-1.5 p-4.5">
              <Button variant="ghost" disabled={!shokoStatus?.signed_in} on:click={fullShokoSync}>{$locale && m.config_btn_shoko_full_sync()}</Button>
              {#if shokoStatus?.last_full_sync && !shokoStatus.last_full_sync.startsWith("0001")}
                <p class="text-caption text-body" data-testid="shoko-last-full">{$locale && m.config_shoko_last_full_sync({ when: new Date(shokoStatus.last_full_sync).toLocaleString() })}</p>
              {/if}
              <p class="text-caption text-subtle">{$locale && m.config_hint_shoko_full_sync()}</p>
            </div>
            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="shoko_full_packs"
                bind:checked={config.shoko_full_packs}
                label={$locale && m.config_label_shoko_full_packs()}
                inline={true}
              />
              <p class="text-caption text-subtle">{$locale && m.config_hint_shoko_full_packs()}</p>
            </div>
            <div class="space-y-1.5 p-4.5" data-testid="shoko-folders">
              <p class="text-copy font-semibold text-heading">{$locale && m.config_label_shoko_folders()}</p>
              <p class="text-caption text-subtle">{$locale && m.config_hint_shoko_folders()}</p>
              {#if shokoStatus?.signed_in && !shokoStatus.import_folders?.length && !shokoStatus.import_folders_error}
                <p class="text-caption text-subtle">{$locale && m.config_shoko_no_import_folder()}</p>
              {/if}
              {#if anyRootInShoko}
                <p class="text-caption text-body" data-testid="shoko-roots-in">{$locale && m.config_shoko_roots_in({ folders: shokoRoots.filter((r) => r.in_shoko).map((r) => r.name).join(", ") })}</p>
                {#if shokoStatus?.last_import_request}
                  <p class="text-caption text-body" data-testid="shoko-last-import">{$locale && m.config_shoko_last_import({ when: new Date(shokoStatus.last_import_request).toLocaleString() })}</p>
                {/if}
                {#if shokoStatus?.import_error}
                  <p class="text-caption text-danger">{$locale && m.config_shoko_import_error({ error: shokoStatus.import_error })}</p>
                {/if}
              {/if}
              {#if shokoStatus?.import_folders?.length}
                <p class="text-caption text-body">{$locale && m.config_shoko_import_folders()}</p>
                <ul class="space-y-0.5" data-testid="shoko-import-folders">
                  {#each shokoStatus.import_folders as folder (folder.id)}
                    <li class="text-caption text-body">
                      <span class="break-all font-mono">{folder.path}</span>
                      · {$locale && shokoFolderKind(folder)}{#if folder.watched}, {$locale && m.config_shoko_folder_watched()}{/if}
                      {#if folder.app_path && trimSlash(folder.app_path) !== trimSlash(folder.path)}
                        · {$locale && m.config_shoko_folder_here()} <span class="break-all font-mono">{folder.app_path}</span>
                      {/if}
                    </li>
                  {/each}
                </ul>
              {/if}
              {#if shokoStatus?.import_folders_error}
                <p class="text-caption text-danger">{$locale && m.config_shoko_import_folders_error({ error: shokoStatus.import_folders_error })}</p>
              {/if}
            </div>
            <div class="space-y-2.5 p-4.5" data-testid="shoko-path-map">
              <div class="space-y-1">
                <p class="text-copy text-body">{$locale && m.config_label_shoko_path_map()}</p>
                <p class="text-caption text-subtle">{$locale && m.config_hint_shoko_path_map()}</p>
              </div>
              {#each config.shoko_path_map ?? [] as mapping, k (k)}
                <div class="flex flex-col gap-2 md:flex-row md:items-end">
                  <div class="flex-1">
                    <Input id={`shoko_map_app_${k}`} label={(T && T.pathMapApp) || ""} bind:value={mapping.app} placeholder="/mnt/anime" />
                  </div>
                  <div class="flex-1">
                    <Input id={`shoko_map_shoko_${k}`} label={$locale && m.config_shoko_path_map_shoko()} bind:value={mapping.shoko} placeholder="/import" />
                  </div>
                  <Button variant="ghost" ariaLabel={(T && T.pathMapRemove) || ""} on:click={() => removeShokoPathMapping(k)}>✕</Button>
                </div>
              {/each}
              <Button variant="ghost" on:click={addShokoPathMapping}>{T && T.pathMapAdd}</Button>
            </div>
          {/if}

          {#if activeGroup === "client"}
            <div class="space-y-1.5 p-4.5">
              <Toggle
                id="use_qbittorrent"
                checked={config.torrent_client === "qbittorrent"}
                on:change={(e) => (config.torrent_client = isOn(e) ? "qbittorrent" : "builtin")}
                label={(T && T.labelUseQBittorrent) || ""}
                inline={true}
              />
              <p class="text-caption text-subtle">{T && T.hintUseQBittorrent}</p>
              {#if config.torrent_client === "qbittorrent" && config.library_mode !== "direct"}
                <p class="text-caption text-danger">{T && T.qbittorrentNeedsDirect}</p>
              {/if}
            </div>

            {#if config.torrent_client === "qbittorrent"}
              <div class="p-4.5">
                <Input
                  id="qbittorrent_url"
                  label={(T && T.labelQBittorrentURL) || ""}
                  subtitle={(T && T.hintQBittorrentURL) || ""}
                  bind:value={config.qbittorrent_url}
                  placeholder="http://127.0.0.1:8080"
                  required={true}
                />
              </div>
              <div class="p-4.5">
                <Input
                  id="qbittorrent_username"
                  label={(T && T.labelQBittorrentUsername) || ""}
                  bind:value={config.qbittorrent_username}
                />
              </div>
              <div class="p-4.5">
                <Input
                  id="qbittorrent_password"
                  type="password"
                  label={(T && T.labelQBittorrentPassword) || ""}
                  subtitle={(T && T.hintQBittorrentPassword) || ""}
                  bind:value={config.qbittorrent_password}
                />
              </div>
              <div class="p-4.5">
                <Input
                  id="qbittorrent_api_key"
                  type="password"
                  label={(T && T.labelQBittorrentAPIKey) || ""}
                  subtitle={(T && T.hintQBittorrentAPIKey) || ""}
                  bind:value={config.qbittorrent_api_key}
                />
              </div>
              <div class="p-4.5">
                <Input
                  id="qbittorrent_category"
                  label={(T && T.labelQBittorrentCategory) || ""}
                  subtitle={(T && T.hintQBittorrentCategory) || ""}
                  bind:value={config.qbittorrent_category}
                />
              </div>
              <div class="space-y-1.5 p-4.5">
                <Toggle
                  id="qbittorrent_manage_settings"
                  bind:checked={config.qbittorrent_manage_settings}
                  label={(T && T.labelQBittorrentManage) || ""}
                  inline={true}
                />
                <p class="text-caption text-subtle">{T && T.hintQBittorrentManage}</p>
              </div>

              <div class="space-y-3 p-4.5">
                <div class="space-y-1">
                  <p class="text-copy text-body">{T && T.labelQBittorrentPathMap}</p>
                  <p class="text-caption text-subtle">{T && T.hintQBittorrentPathMap}</p>
                </div>
                {#each config.qbittorrent_path_map ?? [] as mapping, i (i)}
                  <div class="flex flex-col gap-2 md:flex-row md:items-end">
                    <div class="flex-1">
                      <Input id={`path_map_app_${i}`} label={(T && T.pathMapApp) || ""} bind:value={mapping.app} placeholder="/mnt/anime" />
                    </div>
                    <div class="flex-1">
                      <Input id={`path_map_qbit_${i}`} label={(T && T.pathMapQBittorrent) || ""} bind:value={mapping.qbittorrent} placeholder="/downloads" />
                    </div>
                    <Button variant="ghost" ariaLabel={(T && T.pathMapRemove) || ""} on:click={() => removePathMapping(i)}>✕</Button>
                  </div>
                {/each}
                <Button variant="ghost" on:click={addPathMapping}>{T && T.pathMapAdd}</Button>
              </div>

              <div class="space-y-2 p-4.5">
                <Button variant="ghost" disabled={qbitTesting || !config.qbittorrent_url?.trim()} on:click={runQBitTest}>
                  {qbitTesting ? (T && T.qbittorrentTesting) : (T && T.btnTestQBittorrent)}
                </Button>
                {#if qbitTestResult}
                  <p class="text-caption text-body" data-testid="qbittorrent-test-result">
                    {$locale && m.config_qbittorrent_test_ok({ app: qbitTestResult.app_version, webapi: qbitTestResult.webapi_version })}
                    {#if qbitTestResult.sign_in === "api_key"}
                      {$locale && m.config_qbittorrent_test_signin_key()}
                    {:else if qbitTestResult.sign_in === "password"}
                      {$locale && m.config_qbittorrent_test_signin_password()}
                    {/if}
                    {#if qbitTestResult.library_visible && qbitTestResult.library_path}
                      {$locale && m.config_qbittorrent_test_library({ path: qbitTestResult.library_path })}
                    {/if}
                    {#if qbitTestResult.incomplete_visible && qbitTestResult.incomplete_path}
                      {$locale && m.config_qbittorrent_test_incomplete({ path: qbitTestResult.incomplete_path })}
                    {/if}
                    {#if qbitTestResult.queueing_enabled === false}
                      {$locale && m.config_qbittorrent_test_queue_off()}
                    {:else if qbitTestResult.queueing_enabled && (qbitTestResult.max_active_downloads ?? -1) < 0}
                      {$locale && m.config_qbittorrent_test_queue_unlimited()}
                    {:else if qbitTestResult.queueing_enabled}
                      {$locale && m.config_qbittorrent_test_queue({ count: String(qbitTestResult.max_active_downloads) })}
                    {/if}
                  </p>
                  {#if !config.qbittorrent_manage_settings && (qbitTestResult.disk_io_type === 0 || qbitTestResult.disk_io_type === 1)}
                    <p class="text-caption text-subtle" data-testid="qbittorrent-test-diskio">{$locale && m.config_qbittorrent_test_disk_io()}</p>
                  {/if}
                  {#if qbitTestResult.problem}
                    <p class="text-caption text-danger">{$locale && m.config_qbittorrent_test_problem({ problem: qbitTestResult.problem })}</p>
                  {/if}
                {/if}
                {#if qbitTestError}
                  <p class="text-caption text-danger" data-testid="qbittorrent-test-error">{qbitTestError}</p>
                {/if}
              </div>
            {/if}
          {/if}
        </div>
      </div>

      <!-- Actions. `solid` only on Save: it's the screen's only solid accent (§4.1). -->
      <div class="flex flex-wrap justify-end gap-2.5">
        <Button
          variant="ghost"
          disabled={saving}
          on:click={async () => {
            await triggerCheck();
            window.location.hash = "#/status";
          }}
        >
          {T && T.btnRunCheck}
        </Button>
        <Button type="submit" variant="solid" disabled={saving}>
          {saving ? (T && T.btnSaving) : (T && T.btnSave)}
        </Button>
      </div>
    </form>
  {/if}
</div>
