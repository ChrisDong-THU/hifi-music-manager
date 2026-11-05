export type Tags = {
  title: string;
  artist: string;
  album: string;
  album_artist: string;
  genre: string;
  year: string;
  track: string;
  disc: string;
  lyrics: string;
};
export type Track = {
  id: string;
  path: string;
  tags: Tags;
  format: string;
  codec?: string;
  sample_rate?: number;
  bit_depth?: number;
  channels?: number;
  size: number;
  duration: number;
  hash: string;
  audio_hash: string;
  editable: boolean;
  added: string;
};
export type Draft = {
  has_saved_tags: boolean;
  edit_token: string;
  read_state?: DraftStageState;
  read_error?: string;
  quality_state?: DraftStageState;
  quality_error?: string;
  quality?: Record<string, QualityResult>;
  cover_hash?: string;
  id: string;
  name: string;
  created: string;
  state: string;
  message: string;
  files: Track[];
  parent_id?: string;
  previewed: boolean;
  duplicates?: string[];
};
export type DraftStageState =
  | "queued"
  | "running"
  | "done"
  | "failed"
  | "interrupted";
export type QualityResult = {
  status: "analyzed" | "inconclusive" | "unsupported" | "error";
  message: string;
  sample_rate?: number;
  declared_bits?: number;
  effective_bits?: number;
  stored_bits?: number;
  bit_depth_method?: "stored" | "grid";
  upscaled: boolean;
  possible_upsampling: boolean;
  cutoff_hz?: number;
  bandwidth_hz?: number;
  spectral_windows: number;
  samples: number;
  source_hash: string;
  checked_at: string;
};
export type Operation = {
  sequence: number;
  id: string;
  kind: string;
  label: string;
  created: string;
  completed_at?: string;
  state: "working" | "done" | "failed";
  affected: number;
  skipped?: number;
  error?: string;
};
export type Job = {
  batch_id?: string;
  id: string;
  kind: string;
  state: string;
  message: string;
  done: number;
  total: number;
};
export type LibraryStats = {
  tracks: number;
  artists: number;
  albums: number;
  bytes: number;
  duration: number;
};
export type State = {
  /** Identity of this opening of the library, never a persistent disk ID. */
  library_session: string;
  storage?: {
    ready: boolean;
    message?: string;
    volume_id?: string;
    root?: string;
  } | null;
  /** Runtime database revision, not a release version or persistent identifier. */
  revision: number;
  resource_revision: number;
  cover_revision: number;
  csrf: string;
  mode: string;
  music_path: string;
  uploads_path: string;
  stats: LibraryStats;
  operations: Operation[];
  jobs: Job[];
};
export type Album = {
  key: string;
  title: string;
  artist: string;
  year: string;
  count: number;
  cover_id: string;
  formats: string[];
  hi_res: boolean;
};
export type Artist = {
  name: string;
  biography?: string;
  image_url?: string;
  tracks: number;
  albums: number;
  cover_id: string;
};
export type Preferences = {
  duplicate_policy: "skip" | "keep" | "reject";
};
export const defaultPreferences: Preferences = {
  duplicate_policy: "skip",
};
export const fields: Record<keyof Tags, string> = {
  title: "标题",
  artist: "歌手",
  album: "专辑",
  album_artist: "专辑歌手",
  genre: "流派",
  year: "年份",
  track: "曲序",
  disc: "碟号",
  lyrics: "歌词",
};
export const bytes = (n: number) =>
  n >= 2 ** 30
    ? (n / 2 ** 30).toFixed(2) + " GiB"
    : n >= 2 ** 20
      ? (n / 2 ** 20).toFixed(1) + " MiB"
      : (n / 1024).toFixed(1) + " KiB";
export const duration = (n: number) =>
  n > 0
    ? `${Math.floor(n / 60)}:${String(Math.floor(n % 60)).padStart(2, "0")}`
    : "—";
export const audioQuality = (track: Track) =>
  [
    track.codec || track.format.toUpperCase(),
    track.sample_rate ? `${track.sample_rate / 1000}kHz` : "",
    track.bit_depth ? `${track.bit_depth}bit` : "",
  ]
    .filter(Boolean)
    .join(" · ");
export const filename = (p: string) => p.split("/").pop() || p;
export function draftTitle(b: Draft): string {
  const tags = b.files.map((file) => file.tags);
  if (!tags.length) return b.name;
  const common = (field: keyof Tags) => {
    const value = tags[0][field].trim();
    return value && tags.every((tag) => tag[field].trim() === value)
      ? value
      : "";
  };
  const album = common("album");
  // Uploaded files keep uploads/<original batch ID>/<relative upload path>,
  // including after grouping. A one-track folder still has a nested path.
  const path = b.files[0].path.replaceAll("\\", "/");
  const single = b.files.length === 1 && /^uploads\/[^/]+\/[^/]+$/.test(path);
  const artist = single
    ? tags[0].artist.trim() || tags[0].album_artist.trim()
    : common("album_artist") || common("artist");
  if (!album || !artist) return b.name;
  if (!single) return `${artist}[${album}]`;
  const title = tags[0].title.trim();
  return title ? `${title} - ${artist}[${album}]` : b.name;
}

export type Playback = { track: Track; queue: Track[]; batchID?: string };
