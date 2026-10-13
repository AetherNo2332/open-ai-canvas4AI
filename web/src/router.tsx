import { lazy, Suspense, type ReactNode } from 'react';
import { createBrowserRouter, Navigate, Outlet } from 'react-router';
import { FullScreenLoader } from '@/components/ui/aceternity/full-screen-loader';
import { RequireAuth } from '@/components/auth/require-auth';
import UserLayout from '@/layouts/user-layout';
const CanvasPage=lazy(()=>import('@/pages/canvas'));
const LoginPage=lazy(()=>import('@/pages/auth/login'));
const RegisterPage=lazy(()=>import('@/pages/auth/register'));
const ForgotPasswordPage=lazy(()=>import('@/pages/auth/forgot-password'));
const CanvasProjectPage=lazy(()=>import('@/pages/canvas/project'));
const AssetsPage=lazy(()=>import('@/pages/assets'));
const TasksPage=lazy(()=>import('@/pages/tasks'));
const SkillsPage=lazy(()=>import('@/pages/skills'));
const PluginsPage=lazy(()=>import('@/pages/plugins'));
const EagleLibraryPage=lazy(()=>import('@/pages/plugins/eagle'));
const ProjectsPage=lazy(()=>import('@/pages/projects'));
const ProjectDetailPage=lazy(()=>import('@/pages/projects/detail'));
const DesktopSettingsPage=lazy(()=>import('@/pages/desktop/settings'));
const DesktopModelsPage=lazy(()=>import('@/pages/desktop/models'));
const DesktopStoragePage=lazy(()=>import('@/pages/desktop/storage'));
const DesktopAppearancePage=lazy(()=>import('@/pages/desktop/appearance'));
function deferred(element:ReactNode) {return <Suspense fallback={<FullScreenLoader label="正在打开画布" />}>{element}</Suspense>}
export const router=createBrowserRouter([
    // 公共认证路由(云运行时:未登录访客的工作区请求被 RequireAuth 重定向到 /login?next=)。
    {path:'/login',element:deferred(<LoginPage />)},
    {path:'/register',element:deferred(<RegisterPage />)},
    {path:'/forgot-password',element:deferred(<ForgotPasswordPage />)},
    {element:<RequireAuth><UserLayout><Outlet /></UserLayout></RequireAuth>,children:[
    {path:'/',element:<Navigate to="/canvas" replace />},
    {path:'/canvas',element:deferred(<CanvasPage />)},
    {path:'/canvas/:id',element:deferred(<CanvasProjectPage />)},
    {path:'/assets',element:deferred(<AssetsPage />)},
    {path:'/tasks',element:deferred(<TasksPage />)},
    {path:'/skills',element:deferred(<SkillsPage />)},
    {path:'/plugins',element:deferred(<PluginsPage />)},
    {path:'/plugins/eagle',element:deferred(<EagleLibraryPage />)},
    {path:'/projects',element:deferred(<ProjectsPage />)},
    {path:'/projects/:projectId',element:deferred(<ProjectDetailPage />)},
    {path:'/projects/:projectId/chapters/:chapterId',element:deferred(<ProjectDetailPage />)},
    {path:'/projects/:projectId/workflow/:unitId/:stage',element:deferred(<ProjectDetailPage />)},
    {path:'/settings',element:deferred(<DesktopSettingsPage />)},
    {path:'/settings/models',element:deferred(<DesktopModelsPage />)},
    {path:'/settings/storage',element:deferred(<DesktopStoragePage />)},
    {path:'/settings/appearance',element:deferred(<DesktopAppearancePage />)},
    {path:'*',element:<div className="p-8">此页面不可用。<a href="/canvas">返回画布</a></div>},
]}]);
