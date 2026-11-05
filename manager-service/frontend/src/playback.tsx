import {
  createContext,
  useContext,
  type Dispatch,
  type RefObject,
  type SetStateAction,
} from "react";
import type { Playback } from "./types";

type PlaybackControls = {
  playback: Playback | null;
  playing: boolean;
  audio: RefObject<HTMLAudioElement | null>;
  setPlaying: Dispatch<SetStateAction<boolean>>;
  togglePlayback: () => void;
};

export const PlaybackContext = createContext<PlaybackControls | null>(null);

export function usePlayback() {
  const controls = useContext(PlaybackContext);
  if (!controls) throw new Error("PlaybackContext is missing");
  return controls;
}
