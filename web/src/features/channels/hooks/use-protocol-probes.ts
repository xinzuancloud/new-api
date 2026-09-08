/*
Copyright (C) 2023-2026 QuantumNous

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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import { handleServerError } from '@/lib/handle-server-error'

import {
  protocolProfilesAPI,
  type ProbeReport,
  type ProbeRequest,
} from '../lib/protocol-profiles-api'

// One foreground runner per mounted manager. No timers or background polling.
export function useProtocolProbes() {
  const queryClient = useQueryClient()
  const controller = useRef<AbortController | null>(null)
  const [report, setReport] = useState<ProbeReport | null>(null)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<unknown>(null)
  useEffect(
    () => () => {
      controller.current?.abort()
    },
    []
  )
  const update = (next: ProbeReport) => {
    setReport(next)
    queryClient.setQueryData(['protocol-profiles', 'report', next.id], next)
    void queryClient.invalidateQueries({
      queryKey: ['protocol-profiles', 'reports'],
    })
  }
  const run = useMutation({
    mutationFn: async (initial: ProbeReport) => {
      if (controller.current) return
      const active = new AbortController()
      controller.current = active
      setRunning(true)
      setError(null)
      try {
        let next = initial
        do {
          next = await protocolProfilesAPI.run(next.id, active.signal)
          if (active.signal.aborted) break
          update(next)
        } while (next.status === 'pending' && !active.signal.aborted)
      } finally {
        if (controller.current === active) controller.current = null
        setRunning(false)
      }
    },
    onError: (error) => {
      if (
        error instanceof Error &&
        (error.name === 'CanceledError' || error.name === 'AbortError')
      ) {
        return
      }
      setError(error)
      handleServerError(error)
    },
  })
  const create = useMutation({
    mutationFn: (body: ProbeRequest) => protocolProfilesAPI.create(body),
    onSuccess: update,
    onError: (error) => {
      setError(error)
      handleServerError(error)
    },
  })
  const cancel = useMutation({
    mutationFn: async () => {
      controller.current?.abort()
      if (!report) return null
      return protocolProfilesAPI.cancel(report.id)
    },
    onSuccess: (next) => {
      if (next) update(next)
    },
    onError: (error) => {
      setError(error)
      handleServerError(error)
    },
  })
  return { report, setReport, running, run, create, cancel, error }
}
