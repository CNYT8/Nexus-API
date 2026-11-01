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

import React, { useEffect, useRef } from 'react';
import { Typography } from '@douyinfe/semi-ui';
import MarkdownRenderer from '../common/markdown/MarkdownRenderer';
import { ChevronDown, Brain, Loader2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';

const ThinkingContent = ({
  message,
  finalExtractedThinkingContent,
  thinkingSource,
  onToggleReasoningExpansion,
}) => {
  const { t } = useTranslation();
  const scrollRef = useRef(null);
  const lastContentRef = useRef('');

  const isThinkingStatus =
    message.status === 'loading' || message.status === 'incomplete';
  const headerText =
    isThinkingStatus && !message.isThinkingComplete
      ? t('思考中...')
      : t('思考过程');

  useEffect(() => {
    if (
      scrollRef.current &&
      finalExtractedThinkingContent &&
      message.isReasoningExpanded
    ) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [finalExtractedThinkingContent, message.isReasoningExpanded]);

  useEffect(() => {
    if (!isThinkingStatus) {
      lastContentRef.current = '';
    }
  }, [isThinkingStatus]);

  if (!finalExtractedThinkingContent) return null;

  let prevLength = 0;
  if (isThinkingStatus && lastContentRef.current) {
    if (finalExtractedThinkingContent.startsWith(lastContentRef.current)) {
      prevLength = lastContentRef.current.length;
    }
  }

  if (isThinkingStatus) {
    lastContentRef.current = finalExtractedThinkingContent;
  }

  return (
    <div className='mb-3'>
      <button
        type='button'
        className='inline-flex items-center gap-2 py-1 text-sm transition-colors cursor-pointer bg-transparent border-0'
        style={{ color: 'var(--semi-color-text-2)' }}
        onClick={() => onToggleReasoningExpansion(message.id)}
      >
        {isThinkingStatus && !message.isThinkingComplete ? (
          <Loader2
            size={15}
            className='animate-spin shrink-0'
            style={{ color: 'var(--semi-color-primary)' }}
          />
        ) : (
          <Brain size={15} className='shrink-0' />
        )}
        <Typography.Text
          size='small'
          style={{ color: 'var(--semi-color-text-1)', fontWeight: 500 }}
        >
          {headerText}
        </Typography.Text>
        {thinkingSource && (
          <Typography.Text
            size='small'
            type='tertiary'
            className='hidden sm:inline text-xs'
          >
            ({thinkingSource})
          </Typography.Text>
        )}
        <ChevronDown
          size={15}
          className={`transition-transform duration-200 ${
            message.isReasoningExpanded ? 'rotate-180' : 'rotate-0'
          }`}
        />
      </button>

      {message.isReasoningExpanded && (
        <div
          ref={scrollRef}
          className='nexus-reveal mt-2 pl-3 pr-2 py-2 rounded-lg overflow-x-auto overflow-y-auto thinking-content-scroll'
          style={{
            maxHeight: '280px',
            backgroundColor: 'var(--semi-color-fill-0)',
            borderLeft: '2px solid var(--semi-color-border)',
            color: 'var(--semi-color-text-2)',
          }}
        >
          <div className='prose prose-xs sm:prose-sm max-w-none text-xs sm:text-sm'>
            <MarkdownRenderer
              content={finalExtractedThinkingContent}
              className=''
              animated={isThinkingStatus}
              previousContentLength={prevLength}
            />
          </div>
        </div>
      )}
    </div>
  );
};

export default ThinkingContent;
