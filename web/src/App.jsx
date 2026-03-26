import React, { useEffect, useState } from 'react';
import { motion } from 'framer-motion';

export default function App() {
  const [systemStatus, setSystemStatus] = useState(null);

  useEffect(() => {
    fetch('/api/status')
      .then(res => res.json())
      .then(data => setSystemStatus(data.data))
      .catch(err => console.error("获取系统状态失败", err));
  }, []);

  return (
    <div className="min-h-screen bg-slate-950 flex items-center justify-center p-4 relative overflow-hidden">
      <div className="absolute top-1/4 left-1/4 w-96 h-96 bg-purple-600/20 rounded-full blur-[120px] pointer-events-none" />
      <div className="absolute bottom-1/4 right-1/4 w-96 h-96 bg-blue-600/20 rounded-full blur-[120px] pointer-events-none" />

      <motion.div 
        initial={{ opacity: 0, y: 30, scale: 0.95 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        transition={{ type: "spring", stiffness: 100, damping: 20 }}
        className="relative z-10 p-10 rounded-3xl bg-slate-900/50 backdrop-blur-2xl border border-white/10 shadow-[0_0_50px_rgba(0,0,0,0.5)] max-w-lg w-full text-center"
      >
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          transition={{ delay: 0.3, duration: 1 }}
        >
          <h1 className="text-5xl font-black text-transparent bg-clip-text bg-gradient-to-br from-white via-blue-100 to-purple-400 tracking-tight">
            {systemStatus ? systemStatus.system_name : 'NEXUS'}
          </h1>
          <p className="mt-4 text-slate-400 font-medium tracking-wide">
            企业级极致性能 AI 聚合引擎
          </p>
        </motion.div>

        <div className="mt-10 grid grid-cols-2 gap-4">
          <motion.button 
            whileHover={{ scale: 1.05, backgroundColor: "rgba(59, 130, 246, 0.2)" }}
            whileTap={{ scale: 0.95 }}
            className="py-3 px-6 rounded-xl border border-blue-500/30 text-blue-400 font-semibold transition-colors cursor-pointer"
          >
            控制台登录
          </motion.button>
          <motion.button 
            whileHover={{ scale: 1.05, backgroundColor: "rgba(168, 85, 247, 0.2)" }}
            whileTap={{ scale: 0.95 }}
            className="py-3 px-6 rounded-xl border border-purple-500/30 text-purple-400 font-semibold transition-colors cursor-pointer"
          >
            开发文档
          </motion.button>
        </div>

        {systemStatus && (
          <motion.div 
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ delay: 0.6 }}
            className="mt-8 text-xs text-slate-500 flex justify-between px-4"
          >
            <span>引擎版本: {systemStatus.version}</span>
            <span className="flex items-center gap-1">
              <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
              节点运行极速
            </span>
          </motion.div>
        )}
      </motion.div>
    </div>
  );
}
