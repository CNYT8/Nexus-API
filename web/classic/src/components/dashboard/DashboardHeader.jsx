/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect, useState } from 'react';
import { Button } from '@douyinfe/semi-ui';
import { RefreshCw, Search } from 'lucide-react';

const DashboardHeader = ({
  getGreeting,
  showSearchModal,
  refresh,
  loading,
}) => {
  const ICON_BUTTON_CLASS = 'text-white hover:bg-opacity-80 !rounded-full';
  const [typedGreeting, setTypedGreeting] = useState('');
  const [isTyping, setIsTyping] = useState(false);

  useEffect(() => {
    const characters = Array.from(getGreeting || '');
    let timer;

    if (characters.length === 0) {
      setTypedGreeting('');
      setIsTyping(false);
      return undefined;
    }

    setTypedGreeting(characters[0]);
    setIsTyping(characters.length > 1);
    if (characters.length === 1) {
      return undefined;
    }

    let index = 1;
    const interval = 2000 / (characters.length - 1);
    const typeNextCharacter = () => {
      setTypedGreeting(characters.slice(0, index + 1).join(''));
      index += 1;
      if (index < characters.length) {
        timer = setTimeout(typeNextCharacter, interval);
      } else {
        setIsTyping(false);
      }
    };

    timer = setTimeout(typeNextCharacter, interval);
    return () => clearTimeout(timer);
  }, [getGreeting]);

  return (
    <div className='flex items-center justify-between mb-4'>
      <h2 className='dashboard-greeting-title text-2xl font-semibold'>
        <span aria-label={getGreeting}>{typedGreeting}</span>
        {isTyping && (
          <span className='dashboard-greeting-cursor' aria-hidden='true' />
        )}
      </h2>
      <div className='flex gap-3'>
        <Button
          type='tertiary'
          icon={<Search size={16} />}
          onClick={showSearchModal}
          className={`bg-green-500 hover:bg-green-600 ${ICON_BUTTON_CLASS}`}
        />
        <Button
          type='tertiary'
          icon={<RefreshCw size={16} />}
          onClick={refresh}
          loading={loading}
          className={`bg-blue-500 hover:bg-blue-600 ${ICON_BUTTON_CLASS}`}
        />
      </div>
    </div>
  );
};

export default DashboardHeader;
