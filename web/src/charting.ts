import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { DataZoomComponent, GridComponent, LegendComponent, TooltipComponent, MarkLineComponent, MarkAreaComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([LineChart, DataZoomComponent, GridComponent, LegendComponent, TooltipComponent, MarkLineComponent, MarkAreaComponent, CanvasRenderer])

export default echarts
