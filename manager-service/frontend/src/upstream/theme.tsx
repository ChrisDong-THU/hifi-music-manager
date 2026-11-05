import CssBaseline from "@mui/material/CssBaseline";
import GlobalStyles from "@mui/material/GlobalStyles";
import { StyledEngineProvider } from "@mui/material/styles";
import {
  createTheme,
  alpha,
  darken,
  lighten,
  ThemeProvider as MuiThemeProvider,
} from "@mui/material/styles";
import { useRoute } from "../routing";

// Global styles
import "./main.css";

// TS Augmentation to add custom breakpoints (see below)
declare module "@mui/material/styles" {
  interface BreakpointOverrides {
    xs: false; // removes the `xs` breakpoint
    sm: false;
    md: false;
    lg: false;
    xl: false;
    mobile: true; // adds the `mobile` breakpoint
    tablet: true;
    laptop: true;
    desktop: true;
    hpc: true;
  }

  interface PaletteColor {
    muted?: string;
  }
  interface SimplePaletteColorOptions {
    muted?: string;
  }

  // icon sizes
  interface Theme {
    iconSize: {
      xs: number;
      sm: number;
      md: number;
      lg: number;
      xl: number;
    };
  }
  interface ThemeOptions {
    iconSize?: {
      xs: number;
      sm: number;
      md: number;
      lg: number;
      xl: number;
    };
  }
}

// Page accents have one source. Both MUI color slots and CSS states derive from it.
export const pageAccents = {
  home: "#E7B66B",
  inbox: "#20F5F1",
  library: "#ED41C3",
  search: "#B5D976",
} as const;

const darkTheme = createTheme({
  // Bundle MUI's default typeface and use explicit sans-serif CJK fallbacks.
  typography: {
    fontFamily:
      '"Roboto Variable", Roboto, "Noto Sans SC", "Microsoft YaHei", "PingFang SC", Arial, sans-serif',
  },
  iconSize: {
    xs: 12,
    sm: 16,
    md: 18,
    lg: 20,
    xl: 24,
  },

  palette: {
    mode: "dark",
    tonalOffset: 0.4,
    text: {
      primary: "#ffffff",
      // overwriting secondary fixes the transparency (bad on icons)
      secondary: "#ACB3B9",
    },
    action: {
      // hover: "#212529",
      hover: "#3A3C3E",
      selected: "#7C848E22",
    },
    background: {
      default: "#000000",
      paper: "#101315",
    },
  },
  components: {
    MuiTextField: {
      // Standard forms inherit one contract. Specialized controls (for example
      // the search box) override only the properties their interaction needs.
      defaultProps: {
        variant: "outlined",
        size: "small",
        fullWidth: true,
        color: "secondary",
      },
    },
    MuiCssBaseline: {
      styleOverrides: {
        "button, input, select, textarea, code, pre, kbd, samp": {
          fontFamily: "inherit",
        },
      },
    },
    MuiTooltip: {
      styleOverrides: {
        tooltip: {
          backgroundColor: "#101315",
          borderRadius: "0.3rem",
          border: "1px solid var(--surface-border)",
        },
      },
    },
    MuiMenu: {
      styleOverrides: {
        paper: {
          borderRadius: "0.3rem",
        },
      },
    },
    MuiPaper: {
      styleOverrides: {
        root: {
          // Ensuring consistent dark mode color independent of paper elevation
          backgroundImage: "none",
        },
      },
    },
    MuiIconButton: {
      styleOverrides: {
        root: ({ theme }) => ({
          "&:not(.player-play-pause):hover": {
            color: theme.palette.primary.main,
            backgroundColor: alpha(theme.palette.primary.main, 0.08),
          },
        }),
      },
    },
  },

  breakpoints: {
    values: {
      mobile: 0,
      tablet: 640,
      laptop: 1024,
      desktop: 1200,
      hpc: 1800,
    },
  },

  colorSchemes: {
    dark: true,
  },
});

function createPageTheme(main: string) {
  const accent = {
    main,
    light: lighten(main, 0.4),
    dark: darken(main, 0.25),
    muted: darken(main, 0.4),
    contrastText: "#000000",
  };
  return createTheme(darkTheme, {
    palette: {
      primary: accent,
      secondary: accent,
      action: { hover: alpha(main, 0.045), selected: alpha(main, 0.1) },
    },
  });
}

const pageThemes = {
  home: createPageTheme(pageAccents.home),
  inbox: createPageTheme(pageAccents.inbox),
  library: createPageTheme(pageAccents.library),
  search: createPageTheme(pageAccents.search),
};

export default function ThemeProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const { path } = useRoute();
  const page = Object.hasOwn(pageThemes, path)
    ? (path as keyof typeof pageThemes)
    : "home";
  const theme = pageThemes[page];
  const accent = theme.palette.primary;
  return (
    <StyledEngineProvider injectFirst>
      <MuiThemeProvider theme={theme}>
        <CssBaseline />
        {/* Root variables also reach portaled dialogs, menus and tooltips. */}
        <GlobalStyles
          styles={{
            ":root": {
              "--page-accent": accent.main,
              "--page-accent-light": accent.light,
              "--page-accent-muted": accent.muted,
              "--page-accent-contrast": accent.contrastText,
              "--page-accent-faint": alpha(accent.main, 0.04),
              "--page-accent-soft": alpha(accent.main, 0.06),
              "--page-accent-selected": alpha(accent.main, 0.1),
              "--page-accent-border": alpha(accent.main, 0.17),
              "--page-accent-label": alpha(accent.light, 0.75),
              // Shared opaque material for cards and portaled surfaces.
              "--surface-base": "#080A0C",
              "--surface-raised": theme.palette.background.paper,
              "--surface-background": `radial-gradient(ellipse at top left, ${alpha(accent.main, 0.06)} 0%, transparent 66%), linear-gradient(150deg, #101315, #080A0C)`,
              "--surface-border": "#ffffff0e",
              "--surface-top-border": `color-mix(in srgb, ${accent.main} 18%, #171A1D)`,
              "--surface-shadow": "inset 0 1px 0 #ffffff03, 0 8px 28px #0005",
              ...Object.fromEntries(
                Object.entries(pageAccents).map(([name, color]) => [
                  `--${name}-accent`,
                  color,
                ]),
              ),
            },
          }}
        />
        {children}
      </MuiThemeProvider>
    </StyledEngineProvider>
  );
}
