import React, { useEffect, useState } from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import AuthPage from './pages/AuthPage';
import Dashboard from './pages/Dashboard';

export default function App() {
  const [systemStatus, setSystemStatus] = useState(null);

  useEffect(() => {
    fetch('/api/status')
      .then(res => res.json())
      .then(data => setSystemStatus(data.data))
      .catch(err => console.error("获取系统状态失败", err));
  }, []);

  return (
    <div className="min-h-screen bg-slate-950 text-white font-sans antialiased selection:bg-purple-500/30">
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<Navigate to="/auth" replace />} />
          <Route path="/auth" element={<AuthPage systemName={systemStatus?.system_name || 'NEXUS'} />} />
          <Route path="/dashboard" element={<Dashboard />} />
        </Routes>
      </BrowserRouter>
    </div>
  );
}
