interface VideoCodecCandidate {
  codec: string;
  profiles: string[];
  codecString: string;
  codecString4k: string;
}

interface AudioCodecCandidate {
  codec: string;
  contentType: string;
  maxChannels: number;
}

interface ContainerCandidate {
  container: string;
  contentType: string;
}

export interface VideoCodecCap {
  codec: string;
  profiles?: string[];
  maxHeight?: number;
  maxBitrate?: number;
  maxLevel?: number;
}

export interface AudioCodecCap {
  codec: string;
  maxChannels?: number;
}

export interface CapabilityProfile {
  client: string;
  containers: string[];
  directCodecs: string[];
  videoCodecs: VideoCodecCap[];
  audioCodecs: AudioCodecCap[];
  hdrFormats?: string[];
  subtitleFormats: string[];
  downstreamAudio: string;
}

interface ProbeDeps {
  client?: string;
  document?: Pick<Document, "createElement"> | null;
  navigator?: Pick<Navigator, "mediaCapabilities"> | null;
  video?: Pick<HTMLVideoElement, "canPlayType"> | null;
  mediaCapabilities?: MediaCapabilities | null;
  clientPrefs?: ClientPref[] | null;
}

const VIDEO_CANDIDATES: VideoCodecCandidate[] = [
  {
    codec: "h264",
    profiles: ["Constrained Baseline", "Baseline", "Main", "High"],
    codecString: "avc1.640028",
    codecString4k: "avc1.64002a",
  },
  {
    codec: "hevc",
    profiles: ["Main", "Main 10"],
    codecString: "hev1.2.4.L120.B0",
    codecString4k: "hev1.2.4.L153.B0",
  },
  {
    codec: "vp9",
    profiles: ["Profile 0", "Profile 2"],
    codecString: "vp09.00.10.08",
    codecString4k: "vp09.00.50.08",
  },
  {
    codec: "av1",
    profiles: ["Main"],
    codecString: "av01.0.05M.08",
    codecString4k: "av01.0.13M.08",
  },
];

const AUDIO_CANDIDATES: AudioCodecCandidate[] = [
  { codec: "aac", contentType: 'audio/mp4; codecs="mp4a.40.2"', maxChannels: 2 },
  { codec: "ac3", contentType: 'audio/mp4; codecs="ac-3"', maxChannels: 6 },
  { codec: "eac3", contentType: 'audio/mp4; codecs="ec-3"', maxChannels: 6 },
  { codec: "opus", contentType: 'audio/webm; codecs="opus"', maxChannels: 2 },
  { codec: "flac", contentType: 'audio/mp4; codecs="flac"', maxChannels: 2 },
];

const CONTAINER_CANDIDATES: ContainerCandidate[] = [
  { container: "mp4", contentType: 'video/mp4; codecs="avc1.640028"' },
  { container: "webm", contentType: 'video/webm; codecs="vp9"' },
];

const PROBE_HEIGHT_1080 = 1080;
const PROBE_HEIGHT_4K = 2160;

function canPlayTypeSupported(
  video: Pick<HTMLVideoElement, "canPlayType"> | null,
  contentType: string,
): boolean {
  return video?.canPlayType(contentType) !== "";
}

async function mediaCapabilitiesSupports(
  mediaCapabilities: MediaCapabilities | null,
  configuration: MediaDecodingConfiguration,
): Promise<boolean | null> {
  if (typeof mediaCapabilities?.decodingInfo !== "function") {
    return null;
  }
  try {
    const info = await mediaCapabilities.decodingInfo(configuration);
    return Boolean(info.supported);
  } catch {
    return null;
  }
}

async function probeVideoCodec(
  candidate: VideoCodecCandidate,
  video: Pick<HTMLVideoElement, "canPlayType"> | null,
  mediaCapabilities: MediaCapabilities | null,
): Promise<VideoCodecCap | null> {
  const config4k: MediaDecodingConfiguration = {
    type: "media-source",
    video: {
      contentType: `video/mp4; codecs="${candidate.codecString4k}"`,
      width: 3840,
      height: PROBE_HEIGHT_4K,
      bitrate: 20_000_000,
      framerate: 30,
    },
  };
  const config1080: MediaDecodingConfiguration = {
    type: "media-source",
    video: {
      contentType: `video/mp4; codecs="${candidate.codecString}"`,
      width: 1920,
      height: PROBE_HEIGHT_1080,
      bitrate: 8_000_000,
      framerate: 30,
    },
  };

  const mc4k = await mediaCapabilitiesSupports(mediaCapabilities, config4k);
  const mc1080 = await mediaCapabilitiesSupports(mediaCapabilities, config1080);

  let maxHeight = 0;
  if (mc4k === true) {
    maxHeight = PROBE_HEIGHT_4K;
  } else if (mc1080 === true) {
    maxHeight = PROBE_HEIGHT_1080;
  } else if (
    mc1080 === null &&
    canPlayTypeSupported(video, `video/mp4; codecs="${candidate.codecString}"`)
  ) {
    maxHeight = PROBE_HEIGHT_1080;
  }

  if (maxHeight === 0) {
    return null;
  }
  return { codec: candidate.codec, profiles: candidate.profiles.slice(), maxHeight };
}

async function probeAudioCodec(
  candidate: AudioCodecCandidate,
  video: Pick<HTMLVideoElement, "canPlayType"> | null,
  mediaCapabilities: MediaCapabilities | null,
): Promise<AudioCodecCap | null> {
  const supported = await mediaCapabilitiesSupports(mediaCapabilities, {
    type: "media-source",
    audio: { contentType: candidate.contentType, channels: String(candidate.maxChannels) },
  });
  if (
    supported !== true &&
    !(supported === null && canPlayTypeSupported(video, candidate.contentType))
  ) {
    return null;
  }
  return { codec: candidate.codec, maxChannels: candidate.maxChannels };
}

async function probeHDR(mediaCapabilities: MediaCapabilities | null): Promise<string[]> {
  const checks = [
    { format: "hdr10", transferFunction: "pq" as const },
    { format: "hlg", transferFunction: "hlg" as const },
  ];
  const formats: string[] = [];
  for (const check of checks) {
    const supported = await mediaCapabilitiesSupports(mediaCapabilities, {
      type: "media-source",
      video: {
        contentType: 'video/mp4; codecs="hev1.2.4.L120.B0"',
        width: 1920,
        height: PROBE_HEIGHT_1080,
        bitrate: 8_000_000,
        framerate: 30,
        transferFunction: check.transferFunction,
      },
    });
    if (supported === true) {
      formats.push(check.format);
    }
  }
  return formats;
}

async function downstreamAudioHint(clientPrefs: ClientPref[] | null | undefined): Promise<string> {
  let prefs = clientPrefs;
  if (prefs === undefined) {
    try {
      prefs = await fetchClientPrefs();
    } catch {
      prefs = [];
    }
  }
  return resolvePreferences(prefs ?? []).downstreamAudio === "surround" ? "passthrough" : "stereo";
}

export async function probeCapabilityProfile(deps: ProbeDeps = {}): Promise<CapabilityProfile> {
  const doc = deps.document ?? (typeof document !== "undefined" ? document : null);
  const nav = deps.navigator ?? (typeof navigator !== "undefined" ? navigator : null);
  const client = deps.client ?? "beamfall-web";
  const video = deps.video ?? doc?.createElement("video") ?? null;
  const mediaCapabilities = deps.mediaCapabilities ?? nav?.mediaCapabilities ?? null;

  const containers = CONTAINER_CANDIDATES.filter((candidate) =>
    canPlayTypeSupported(video, candidate.contentType),
  ).map((candidate) => candidate.container);

  const videoCodecs: VideoCodecCap[] = [];
  const directCodecs: string[] = [];
  for (const candidate of VIDEO_CANDIDATES) {
    const cap = await probeVideoCodec(candidate, video, mediaCapabilities);
    if (cap) {
      videoCodecs.push(cap);
      directCodecs.push(cap.codec);
    }
  }

  const audioCodecs: AudioCodecCap[] = [];
  for (const candidate of AUDIO_CANDIDATES) {
    const cap = await probeAudioCodec(candidate, video, mediaCapabilities);
    if (cap) {
      audioCodecs.push(cap);
    }
  }

  const [hdrFormats, downstreamAudio] = await Promise.all([
    probeHDR(mediaCapabilities),
    downstreamAudioHint(deps.clientPrefs),
  ]);

  const profile: CapabilityProfile = {
    client,
    containers,
    directCodecs,
    videoCodecs,
    audioCodecs,
    subtitleFormats: ["srt", "webvtt"],
    downstreamAudio,
  };
  if (hdrFormats.length > 0) {
    profile.hdrFormats = hdrFormats;
  }
  return profile;
}

import { fetchClientPrefs } from "@/lib/graphql";
import type { ClientPref } from "@/lib/graphql/types";
import { resolvePreferences } from "./preferences";
