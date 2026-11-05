// Adapted from metasauce/beets-flask 00b246d, MIT.
// Source: components/frontpage/statsCard.tsx and inbox/cards/common.tsx.
import type { ReactNode } from "react";
import { Avatar, Box, Divider, Typography, type BoxProps } from "@mui/material";
import { alpha } from "@mui/material/styles";

export function AccentHeader({
  icon,
  children,
  color = "primary.main",
  dividerPos = "50%",
  size = "medium",
  ...props
}: {
  icon: ReactNode;
  children?: ReactNode;
  color?: string;
  dividerPos?: string;
  size?: "medium" | "large";
} & BoxProps) {
  const wh = size === "large" ? 52 : 40;
  return (
    <Box sx={{ position: "relative", flexGrow: 1 }} {...props}>
      <Divider
        sx={{
          position: "absolute",
          top: `calc(${dividerPos} - 1px)`,
          width: "100%",
          backgroundColor: color,
          borderBottomWidth: 2,
        }}
      />
      <Box
        sx={{
          pl: 4,
          pr: 1,
          display: "flex",
          alignItems: "flex-end",
          justifyContent: "space-between",
          minWidth: 0,
          zIndex: 1,
          position: "relative",
        }}
      >
        <Avatar
          variant="rounded"
          sx={{
            width: wh,
            height: wh,
            flexShrink: 0,
            bgcolor: color,
            color: "black",
          }}
        >
          {icon}
        </Avatar>
        {children}
      </Box>
    </Box>
  );
}

// Match the inbox header's icon, divider and body2 text placement.
export function SectionHeader({
  icon,
  title,
  color = "primary.main",
}: {
  icon: ReactNode;
  title: string;
  color?: string;
}) {
  return (
    <AccentHeader icon={icon} color={color} dividerPos="70%">
      <Box
        className="section-header-content"
        sx={{ flex: 1, minWidth: 0, pl: 1, pb: 2.5, pr: 1 }}
      >
        <Typography component="h2" variant="body2" fontWeight="bold" noWrap>
          {title}
        </Typography>
      </Box>
    </AccentHeader>
  );
}
export function DirectoryHeader({
  icon,
  path,
  actions,
  color = "primary.main",
}: {
  icon: ReactNode;
  path: string;
  actions?: ReactNode;
  color?: string;
}) {
  const segments = path.replaceAll("\\", "/").split("/").filter(Boolean);
  return (
    <AccentHeader icon={icon} color={color} dividerPos="70%">
      <Box sx={{ flex: 1, minWidth: 0, pl: 1, pr: 1, pb: 2.5 }}>
        <Box
          component="h2"
          title={path}
          aria-label={path}
          sx={{
            m: 0,
            display: "flex",
            alignItems: "flex-end",
            flexWrap: { mobile: "nowrap", laptop: "wrap" },
            columnGap: 0.5,
            rowGap: 0.1,
            overflow: "hidden",
          }}
        >
          {segments.map((segment, index) => (
            <Typography
              key={index}
              component="span"
              variant="body2"
              sx={{ display: "inline-flex", whiteSpace: "nowrap" }}
            >
              {index === 0 && /^[a-z]:$/i.test(segment)
                ? segment
                : `/ ${segment}`}
            </Typography>
          ))}
        </Box>
      </Box>
      {actions ? (
        <Box sx={{ alignSelf: "flex-start", flexShrink: 0, pl: 1 }}>
          {actions}
        </Box>
      ) : null}
    </AccentHeader>
  );
}

export function StatsGrid({
  children,
  columns,
  color = "primary.light",
}: {
  children: ReactNode;
  columns: number;
  color?: string;
}) {
  return (
    <Box
      component="dl"
      className="stats-grid"
      sx={{
        display: "grid",
        gridTemplateColumns: {
          mobile: "repeat(2, minmax(0, 1fr))",
          tablet: `repeat(${columns}, minmax(0, 1fr))`,
        },
        gap: 1.5,
        m: 0,
        "& dt svg": { color },
        ...(columns % 2 === 1
          ? {
              "& > :last-child": {
                gridColumn: { mobile: "1 / -1", tablet: "auto" },
              },
            }
          : {}),
      }}
    >
      {children}
    </Box>
  );
}

export function StatItem({
  title,
  icon,
  value,
  unit,
}: {
  title: string;
  icon: ReactNode;
  value: ReactNode;
  unit?: string;
}) {
  return (
    <Box
      sx={{
        minWidth: 0,
        p: { mobile: 1.5, tablet: 2 },
        background: (theme) =>
          `linear-gradient(145deg, ${alpha(theme.palette.primary.main, 0.075)}, ${alpha(theme.palette.primary.main, 0.02)})`,
        borderRadius: 3,
      }}
    >
      <Typography
        component="dt"
        sx={{
          fontSize: 13,
          fontWeight: 400,
          display: "flex",
          alignItems: "center",
          gap: 1,
          color: "text.secondary",
        }}
      >
        <Box
          sx={{
            width: 28,
            height: 28,
            flexShrink: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            bgcolor: (theme) => alpha(theme.palette.primary.main, 0.1),
            borderRadius: 1.5,
            "& svg": { width: 16, height: 16 },
          }}
        >
          {icon}
        </Box>
        {title}
      </Typography>
      <Typography
        component="dd"
        sx={{
          m: 0,
          mt: 1.25,
          display: "flex",
          alignItems: "baseline",
          flexWrap: "wrap",
          columnGap: 0.75,
          rowGap: 0.25,
          fontSize: { mobile: 24, tablet: 28 },
          lineHeight: 1.2,
          fontWeight: 500,
          fontVariantNumeric: "tabular-nums",
          letterSpacing: "-0.02em",
          overflowWrap: "anywhere",
          color: "text.primary",
        }}
      >
        <Box component="span" sx={{ minWidth: 0 }}>
          {value}
        </Box>
        {unit ? (
          <Box
            component="span"
            sx={{
              fontSize: 12,
              fontWeight: 400,
              letterSpacing: 0,
              color: "text.secondary",
            }}
          >
            {unit}
          </Box>
        ) : null}
      </Typography>
    </Box>
  );
}
