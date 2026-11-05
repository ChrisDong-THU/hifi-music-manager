// Layout and tab styling adapted from beets-flask components/frontpage/navbar.tsx (MIT).
import { Home, Inbox, Library, Search } from "lucide-react";
import { pageAccents } from "./theme";
import {
  Box,
  Tab,
  Tabs,
  styled,
  tabClasses,
  tabsClasses,
  Typography,
} from "@mui/material";

const StyledTabs = styled(Tabs)(({ theme }) => ({
  color: "inherit",
  overflow: "visible",
  display: "flex",
  width: "100%",
  justifyContent: "center",
  [`& .${tabsClasses.indicator}`]: {
    position: "absolute",
    top: "calc(50% - 8px)",
    height: 16,
    filter: "blur(50px)",
    backgroundColor: `var(--nav-accent, ${theme.palette.secondary.main})`,
    zIndex: -1,
  },
  "& .MuiTabs-scroller": { width: "100%", overflow: "visible!important" },
  "& .MuiTabs-list": {
    width: "100%",
    gap: 4,
    justifyContent: "center",
    [theme.breakpoints.up("laptop")]: { gap: 30 },
  },
  [theme.breakpoints.down("laptop")]: {
    "& .MuiTabs-list": {
      display: "grid",
      gridTemplateColumns: "repeat(4,1fr)",
      alignItems: "center",
      justifyItems: "center",
    },
    background: "linear-gradient(to bottom,transparent,black)",
  },
}));
const StyledTab = styled(Tab)(({ theme }) => ({
  lineHeight: "inherit",
  marginTop: 7,
  minHeight: 32,
  minWidth: 0,
  flexDirection: "row",
  letterSpacing: 1,
  justifyContent: "center",
  gap: ".5rem",
  textTransform: "uppercase",
  overflow: "visible",
  transition: "color .3s linear",
  "& svg": { width: 16, height: 16 },
  [`& .${tabClasses.icon}`]: { marginBottom: 0 },
  "&:hover": {
    color: `var(--nav-accent, ${theme.palette.secondary.main})`,
    textShadow: `0 0 50px var(--nav-accent, ${theme.palette.secondary.main})`,
  },
  "&.Mui-selected": {
    color: `var(--nav-accent, ${theme.palette.secondary.main})`,
  },
  "&:focus-visible": {
    outlineColor: `var(--nav-accent, ${theme.palette.secondary.main})`,
  },
  [theme.breakpoints.down("laptop")]: {
    marginTop: 0,
    height: 74,
    display: "flex",
    flexDirection: "column",
    "& svg": { width: 20, height: 20 },
  },
})) as typeof Tab;
const items = [
  { id: "home", label: "首页", Icon: Home },
  { id: "inbox", label: "草稿", Icon: Inbox },
  { id: "library", label: "曲库", Icon: Library },
  { id: "search", label: "搜索", Icon: Search },
];
function pageAccent(page: string) {
  return pageAccents[page as keyof typeof pageAccents] || pageAccents.home;
}
export function NavBar({ path }: { path: string }) {
  const current = items.findIndex((item) => item.id === path);
  return (
    <Box
      component="nav"
      aria-label="主导航"
      sx={(theme) => ({
        "--nav-accent": pageAccent(path),
        position: "fixed",
        bottom: 0,
        zIndex: 30,
        width: "100dvw",
        height: 74,
        display: "flex",
        justifyContent: "center",
        alignItems: "flex-start",
        backdropFilter: "blur(25px)",
        [theme.breakpoints.up("laptop")]: {
          top: 0,
          bottom: "auto",
          borderBottom: "1px solid",
          borderColor: "divider",
          height: 48,
        },
      })}
    >
      <StyledTabs value={current < 0 ? false : current} aria-label="页面导航">
        {items.map(({ id, label, Icon }) => (
          <StyledTab
            key={id}
            component="a"
            href={"#" + id}
            aria-label={label}
            aria-current={id === path ? "page" : undefined}
            sx={{ "--nav-accent": pageAccent(id) }}
            icon={<Icon />}
            label={
              <Typography
                component="span"
                sx={(theme) => ({
                  ml: 1,
                  lineHeight: "12px",
                  [theme.breakpoints.down("laptop")]: {
                    ml: 0,
                    fontSize: theme.typography.caption.fontSize,
                    lineHeight: "inherit",
                    textAlign: "center",
                  },
                })}
              >
                {label}
              </Typography>
            }
          />
        ))}
      </StyledTabs>
    </Box>
  );
}
