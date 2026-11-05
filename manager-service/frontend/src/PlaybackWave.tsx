/** A shared playback indicator; CSS handles motion without a render timer. */
export function PlaybackWave({ active }: { active: boolean }) {
  return (
    <span className="playback-wave" data-active={active} aria-hidden="true">
      <span />
      <span />
      <span />
      <span />
    </span>
  );
}
