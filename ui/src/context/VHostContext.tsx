import React, { createContext, useContext, useState } from 'react';

interface VHostContextType {
  selectedVHost: string; // 'all' or specific vhost name
  setSelectedVHost: (vhost: string) => void;
  availableVHosts: string[];
  setAvailableVHosts: (vhosts: string[]) => void;
}

const VHostContext = createContext<VHostContextType | undefined>(undefined);

export const VHostProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [selectedVHost, setSelectedVHostState] = useState<string>(() => {
    return localStorage.getItem('hermod_selected_vhost') || 'all';
  });
  const [availableVHosts, setAvailableVHosts] = useState<string[]>([]);

  const setSelectedVHost = (vhost: string) => {
    setSelectedVHostState(vhost);
    localStorage.setItem('hermod_selected_vhost', vhost);
  };

  return (
    <VHostContext.Provider value={{ selectedVHost, setSelectedVHost, availableVHosts, setAvailableVHosts }}>
      {children}
    </VHostContext.Provider>
  );
};

export const useVHost = () => {
  const context = useContext(VHostContext);
  if (context === undefined) {
    throw new Error('useVHost must be used within a VHostProvider');
  }
  return context;
};
