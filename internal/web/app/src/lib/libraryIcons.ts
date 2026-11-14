import {
  BookOpen,
  Camera,
  Film,
  Folder,
  Headphones,
  Heart,
  Image,
  Layers,
  type LucideIcon,
  MicVocal,
  Music,
  Podcast,
  Sparkles,
  Star,
  Tv,
  Users,
  Video,
} from "lucide-react";

// The Library icon vocabulary (CORE-1200): the fixed set of symbol names the
// server accepts for a Library and publishes as `icon`. The names are the wire
// contract; which drawing stands for each one is this client's choice. A name
// this build does not know (a newer server) has no entry, and callers fall
// back to what they drew before icons existed.
const ICONS = {
  film: Film,
  tv: Tv,
  video: Video,
  note: Music,
  mic: MicVocal,
  broadcast: Podcast,
  headphones: Headphones,
  book: BookOpen,
  image: Image,
  camera: Camera,
  people: Users,
  star: Star,
  heart: Heart,
  sparkles: Sparkles,
  layers: Layers,
  folder: Folder,
} satisfies Record<string, LucideIcon>;

export type LibraryIconName = keyof typeof ICONS;

/** Every icon name, in the order a picker offers them. */
export const LIBRARY_ICON_NAMES = Object.keys(ICONS) as LibraryIconName[];

/** The symbol for an icon name, or undefined when this client does not know it. */
export function libraryIcon(name: string | null | undefined): LucideIcon | undefined {
  return name && Object.hasOwn(ICONS, name) ? ICONS[name as LibraryIconName] : undefined;
}
