import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import {
  Alert,
  Box,
  Button,
  Card,
  CircularProgress,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import { apiFetch, jsonResponse } from "./http-client";
import { navigate, useRoute } from "./routing";
import ThemeProvider from "./upstream/theme";
import { useContentRelay } from "./useContentRelay";

type AuthStatus = {
  authenticated: boolean;
};

async function authRequest(path: string, body?: unknown, signal?: AbortSignal) {
  return jsonResponse<AuthStatus>(await apiFetch("auth/" + path, body, "", { signal }));
}

export function ManagerGate({
  children,
}: {
  children: (onSessionExpired: () => void) => ReactNode;
}) {
  const registering = useRoute().path === "register";
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const authView = status && !status.authenticated ? (registering ? "register" : "login") : null;
  const authRelay = useContentRelay(authView, { direction: registering ? 1 : -1 });

  const applyStatus = useCallback((next: AuthStatus) => {
    const hash = location.hash.split("?")[0];
    if (next.authenticated && (hash === "#login" || hash === "#register")) navigate("home");
    setStatus(next);
  }, []);

  useEffect(() => {
    const request = new AbortController();
    void authRequest("status", undefined, request.signal)
      .then((next) => {
        if (!request.signal.aborted) applyStatus(next);
      })
      .catch((e) => {
        if (!request.signal.aborted) setError(e instanceof Error ? e.message : "无法连接管理服务");
      });
    return () => request.abort();
  }, [applyStatus, retry]);

  const onSessionExpired = useCallback(() => {
    if (location.hash.split("?")[0] === "#register") navigate("login");
    setStatus(null);
    setError("");
    setRetry((count) => count + 1);
  }, []);

  return (
    <ThemeProvider pageOverride={status?.authenticated ? undefined : "home"}>
      {status?.authenticated ? children(onSessionExpired) : (
        <Box
          component="main"
          sx={{
            display: "grid",
            gridTemplateRows: "minmax(min-content, 1fr)",
            justifyItems: "center",
            overflowY: "auto",
            px: 2,
            py: { mobile: 3, tablet: 5 },
            background: "radial-gradient(ellipse at 50% 27%, var(--page-accent-soft), transparent 55%)",
          }}
        >
          <Card
            ref={authRelay}
            aria-labelledby="manager-login-title"
            sx={{
              alignSelf: "center",
              width: "100%",
              maxWidth: registering ? 440 : 420,
              borderRadius: 2,
              p: { mobile: 3, tablet: 4 },
            }}
          >
            <Stack
              data-relay-beat="0"
              spacing={0.75}
              sx={{ alignItems: "center", textAlign: "center", mb: 3.5 }}
            >
              <Typography component="p" sx={{ fontSize: "clamp(1.45rem, 5.8vw, 2rem)", lineHeight: 1.12 }}>
                <span className="app-wordmark">hifi-music-nano</span>
              </Typography>
              <Typography id="manager-login-title" component="h1" variant="body1" fontWeight={500} color="text.secondary">
                {registering ? "注册管理账号" : "音乐库管理登录"}
              </Typography>
            </Stack>
            {status ? (
              <LoginForm
                key={String(registering)}
                registering={registering}
                onSuccess={applyStatus}
              />
            ) : error ? (
              <Alert
                severity="error"
                action={<Button color="inherit" size="small" onClick={onSessionExpired}>重试</Button>}
              >
                {error}
              </Alert>
            ) : (
              <Stack direction="row" spacing={1.5} sx={{ alignItems: "center", justifyContent: "center" }} role="status">
                <CircularProgress size={17} aria-hidden="true" />
                <Typography variant="body2" color="text.secondary">正在连接管理服务</Typography>
              </Stack>
            )}
          </Card>
        </Box>
      )}
    </ThemeProvider>
  );
}

function LoginForm({ registering, onSuccess }: { registering: boolean; onSuccess: (status: AuthStatus) => void }) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [failedLogins, setFailedLogins] = useState(0);
  const submitting = useRef<AbortController | null>(null);
  const rejected = !registering && Boolean(error) && failedLogins > 0;

  useEffect(() => () => submitting.current?.abort(), []);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submitting.current) return;
    const fields = new FormData(event.currentTarget);
    const username = String(fields.get("username") || "");
    const password = String(fields.get("password") || "");
    if (registering && password !== fields.get("confirmPassword")) {
      setError("两次输入的密码不一致");
      return;
    }
    setError("");
    const request = new AbortController();
    submitting.current = request;
    setPending(true);
    try {
      const next = await authRequest(registering ? "register" : "login", {
        username,
        password,
        ...(registering ? { invite_code: String(fields.get("inviteCode") || "") } : {}),
      }, request.signal);
      if (!request.signal.aborted) onSuccess(next);
    } catch (e) {
      if (!request.signal.aborted) {
        setError(e instanceof Error ? e.message : "无法连接管理服务");
        if (!registering) setFailedLogins((count) => count + 1);
      }
    } finally {
      submitting.current = null;
      if (!request.signal.aborted) setPending(false);
    }
  }

  return (
    <Stack
      component="form"
      method="post"
      onSubmit={submit}
      spacing={2}
      aria-busy={pending}
      sx={{
        "& .MuiOutlinedInput-input": { py: "12.5px" },
        "& .MuiInputLabel-sizeSmall:not(.MuiInputLabel-shrink)": {
          transform: "translate(14px, 13px)",
        },
      }}
    >
      {registering && (
        <TextField
          data-relay-beat="1"
          id="manager-invite-code"
          name="inviteCode"
          label="邀请码"
          type="password"
          autoComplete="off"
          required
          disabled={pending}
          slotProps={{ htmlInput: { maxLength: 96, spellCheck: false } }}
        />
      )}
      <TextField
        data-relay-beat="2"
        id="manager-username"
        name="username"
        label="账号"
        autoComplete="username"
        required
        disabled={pending}
        slotProps={{ htmlInput: { maxLength: 64, autoCapitalize: "none", spellCheck: false } }}
      />
      <TextField
        data-relay-beat="3"
        key={failedLogins}
        id="manager-password"
        name="password"
        label="密码"
        type="password"
        autoComplete={registering ? "new-password" : "current-password"}
        required
        disabled={pending}
        error={rejected}
        autoFocus={rejected}
        onChange={error ? () => setError("") : undefined}
        slotProps={{ htmlInput: { maxLength: 256 } }}
        sx={rejected ? {
          animation: "manager-password-rejected 320ms ease-out",
          "@keyframes manager-password-rejected": {
            "0%, 100%": { transform: "translateX(0)" },
            "20%, 60%": { transform: "translateX(-5px)" },
            "40%, 80%": { transform: "translateX(5px)" },
          },
          "@media (prefers-reduced-motion: reduce)": { animation: "none" },
        } : undefined}
      />
      {registering && (
        <TextField
          data-relay-beat="4"
          id="manager-confirm-password"
          name="confirmPassword"
          label="确认密码"
          type="password"
          autoComplete="new-password"
          required
          disabled={pending}
          slotProps={{ htmlInput: { maxLength: 256 } }}
        />
      )}
      {error && <Alert severity="error">{error}</Alert>}
      <Stack
        data-relay-beat="5"
        direction="row"
        spacing={1.5}
        justifyContent="center"
        sx={{
          "& .MuiButton-root": {
            width: 104,
            minWidth: 0,
            minHeight: 44,
            px: 1.5,
            borderRadius: 2,
            fontWeight: 600,
          },
        }}
      >
        <Button
          type="submit"
          variant="contained"
          disableElevation
          disabled={pending}
        >
          {registering ? (pending ? "正在注册…" : "注册登录") : (pending ? "正在验证…" : "登录")}
        </Button>
        <Button
          href={registering ? "#login" : "#register"}
          variant="outlined"
          disabled={pending}
        >
          {registering ? "已有账号" : "注册"}
        </Button>
      </Stack>
    </Stack>
  );
}
