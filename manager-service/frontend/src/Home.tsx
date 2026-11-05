// Layout adapted from beets-flask routes/_frontpage/index.tsx and statsCard.tsx.
import { useState } from "react";
import {
  Alert,
  Box,
  Button,
  Card,
  CardActions,
  CardContent,
  Divider,
} from "@mui/material";
import {
  AudioLines,
  Disc3,
  HardDrive,
  Library,
  RefreshCw,
  SquareArrowOutUpRight,
  Timer,
  UserRound,
} from "lucide-react";
import { useAppActions, useAppState } from "./api";
import { bytes, duration, type LibraryStats } from "./types";
import { SectionHeader, StatItem, StatsGrid } from "./upstream/primitives";
import { HistoryCard } from "./HistoryCard";
import { IconAction, PageTitle } from "./components";
import { DataDiskSettings } from "./DataDiskSettings";
import { useScanStats } from "./useScanStats";
import { useAnimatedStats } from "./useAnimatedStats";

// Only the number tiles render on animation frames; page actions and history
// remain independent of the counter's display refresh rate.
function MusicStats({
  target,
  cycle,
}: {
  target: LibraryStats | null;
  cycle: number;
}) {
  const stats = useAnimatedStats(target, cycle);
  const [sizeValue, sizeUnit] = bytes(stats.bytes).split(" ");
  return (
    <StatsGrid columns={5}>
      <StatItem
        title="时长"
        icon={<Timer />}
        value={duration(stats.duration)}
      />
      <StatItem
        title="大小"
        icon={<HardDrive />}
        value={sizeValue}
        unit={sizeUnit}
      />
      <StatItem
        title="曲目"
        icon={<AudioLines />}
        value={Math.floor(stats.tracks)}
      />
      <StatItem
        title="专辑"
        icon={<Disc3 />}
        value={Math.floor(stats.albums)}
      />
      <StatItem
        title="歌手"
        icon={<UserRound />}
        value={Math.floor(stats.artists)}
      />
    </StatsGrid>
  );
}

export function HomePage() {
  const { state } = useAppState();
  const { enqueue } = useAppActions();
  const [submitting, setSubmitting] = useState(false);
  const [cycle, setCycle] = useState(0);
  const scanning = Boolean(
    state?.jobs.some(
      (job) => job.kind === "scan" && ["queued", "running"].includes(job.state),
    ),
  );
  const liveStats = useScanStats(scanning);
  if (!state) return null;
  async function rescan() {
    if (submitting || scanning) return;
    setCycle((value) => value + 1);
    setSubmitting(true);
    try {
      await enqueue("scan");
    } catch {
      // The shared task notice reports submission failures.
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <>
      <PageTitle
        left={
          <IconAction
            label="重新统计曲库"
            disabled={submitting || scanning || state.storage?.ready === false}
            onClick={() => void rescan()}
          >
            <RefreshCw size={20} />
          </IconAction>
        }
        right={<DataDiskSettings />}
      />
      {state.storage && !state.storage.ready ? (
        <Alert severity="error" sx={{ mb: 2 }}>
          {state.storage.message || "请先选择数据盘"}
        </Alert>
      ) : null}
      <Box className="home-workspace">
        <Card
          className="home-music-card"
          component="section"
          aria-label="我的音乐"
          aria-busy={scanning}
          sx={{ p: { mobile: 1, tablet: 2 } }}
        >
          <SectionHeader icon={<Library size={24} />} title="我的音乐" />
          <CardContent
            sx={{
              px: { mobile: 0.5, tablet: 1 },
              pt: 1.5,
              mt: 1,
              pb: 1.5,
            }}
          >
            <MusicStats
              target={submitting || scanning ? liveStats : state.stats}
              cycle={cycle}
            />
          </CardContent>
          <Divider sx={{ mx: 1 }} />
          <CardActions sx={{ pt: 2, px: 1, justifyContent: "flex-end" }}>
            <Button
              variant="contained"
              color="secondary"
              disabled={state.storage?.ready === false}
              startIcon={<SquareArrowOutUpRight size={18} />}
              href="#library"
            >
              浏览曲库
            </Button>
          </CardActions>
        </Card>
        <HistoryCard />
      </Box>
    </>
  );
}
