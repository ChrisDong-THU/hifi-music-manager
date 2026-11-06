import { createRoot } from "react-dom/client";
import "@fontsource-variable/roboto/wght.css";
import { ManagerGate } from "./ManagerLogin";
import { AppProvider } from "./api";
import { App } from "./App";
import { NotificationsProvider } from "./Notifications";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <ManagerGate>
    {(onSessionExpired) => (
      <NotificationsProvider>
        <AppProvider onSessionExpired={onSessionExpired}>
          <App />
        </AppProvider>
      </NotificationsProvider>
    )}
  </ManagerGate>,
);
