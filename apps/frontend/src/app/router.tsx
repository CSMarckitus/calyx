import { Navigate, Outlet, Route, Routes, useLocation } from "react-router";
import type { components } from "../api/generated/types.ts";
import { useMe } from "../api/queries.ts";
import { unknownManager } from "../domain/errors.ts";
import { AccountScreen } from "../screens/account.tsx";
import { ApiKeysScreen } from "../screens/api-keys.tsx";
import { ClientScreen } from "../screens/client.tsx";
import { ConnectScreen } from "../screens/connect.tsx";
import { ForgotPassword } from "../screens/forgot-password.tsx";
import { InstanceDrawer } from "../screens/instance-drawer.tsx";
import { InstancesScreen } from "../screens/instances.tsx";
import { Invite } from "../screens/invite.tsx";
import { Login } from "../screens/login.tsx";
import { NotFound } from "../screens/not-found.tsx";
import { OrganizationScreen } from "../screens/organization.tsx";
import { ResetPassword } from "../screens/reset-password.tsx";
import { Terms } from "../screens/terms.tsx";
import { Boundary } from "./boundary.tsx";
import { Layout } from "./layout.tsx";
import { isUnauthorized, loginPath } from "./session.tsx";

type Me = components["schemas"]["Me"];

// The shell of a logged-in person. A person with no login gets the screen with no header.
function Shell() {
  const me = useMe();
  if (me.isPending) return null;
  if (me.isError && isUnauthorized(me.error)) {
    return (
      <main className="mx-auto grid w-full max-w-[760px] gap-6 px-4 py-10">
        <Boundary>
          <Outlet />
        </Boundary>
      </main>
    );
  }
  // With no answer of getMe the role is not known. The texts are then the ones of the client user.
  const data: Me | undefined = me.data;
  const viewer = data?.user.role === "owner" ? "owner" : "client";
  return (
    <Layout
      viewer={viewer}
      manager={data?.organization.name ?? unknownManager}
      terms={
        viewer === "owner" && data?.organization.whatsapp_web_acknowledged_at === null
          ? { blocked: data.organization.whatsapp_web_allowed === false }
          : null
      }
    >
      <Outlet />
    </Layout>
  );
}

// The paths for logged-in people. With no user, they go to the login, which returns here.
function LoggedIn() {
  const me = useMe();
  const where = useLocation();
  if (me.isError && isUnauthorized(me.error)) return <Navigate to={loginPath(where)} replace />;
  return <Outlet />;
}

// The paths of the owner. A client user sees NotFound, as the API answers 404 to it too.
function OwnerOnly() {
  const me = useMe();
  if (!me.data) return null;
  return me.data.user.role === "owner" ? <Outlet /> : <NotFound />;
}

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/forgot-password" element={<ForgotPassword />} />
      <Route path="/reset-password" element={<ResetPassword />} />
      <Route path="/invite" element={<Invite />} />
      <Route element={<Shell />}>
        <Route element={<LoggedIn />}>
          {/* The list stays mounted under its drawers, so it keeps its position. */}
          <Route element={<InstancesScreen />}>
            <Route path="/" element={null} />
            {/* Also `/instances/new`: one route, so "Connect" stays mounted when the address
                changes to the new instance. */}
            <Route path="/instances/:id" element={<InstanceDrawer />} />
            {/* A view of an instance. The drawer checks the value of `view`. */}
            <Route path="/instances/:id/:view" element={<InstanceDrawer />} />
          </Route>
          <Route path="/clients/:id" element={<ClientScreen />} />
          <Route path="/developers/api-keys" element={<ApiKeysScreen />} />
          <Route path="/developers/connect" element={<ConnectScreen />} />
          <Route path="/account" element={<AccountScreen />} />
          <Route element={<OwnerOnly />}>
            <Route path="/organization" element={<OrganizationScreen />} />
            <Route path="/organization/whatsapp-web" element={<Terms />} />
          </Route>
        </Route>
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
