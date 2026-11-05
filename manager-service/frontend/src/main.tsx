import { createRoot } from "react-dom/client";
import "@fontsource-variable/roboto/wght.css";
import ThemeProvider from "./upstream/theme";
import { AppProvider } from "./api";
import { App } from "./App";
import { NotificationsProvider } from "./Notifications";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <ThemeProvider>
    <NotificationsProvider>
      <AppProvider>
        <App />
      </AppProvider>
    </NotificationsProvider>
  </ThemeProvider>,
);
